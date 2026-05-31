// Package upgrade は実行中の agmsg バイナリを GitHub Releases の最新版で置き換える。
//
// プラットフォームに合致した tar.gz を取得し、checksums.txt と SHA-256 を突き合わせ、
// アーカイブから agmsg バイナリを取り出して、実行ファイルのパスへ atomically に rename する。
//
// darwin / linux では実行中イメージが mapping され続けるため、パスへの rename は安全
// (.goreleaser.yaml の対象 OS もこの 2 つ)。windows はリリース対象外なので未対応。
package upgrade

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	releaseAPI    = "https://api.github.com/repos/ishii1648/agmsg-go/releases/latest"
	checksumsName = "checksums.txt"
	// projectName は GoReleaser の name_template ({{ .ProjectName }}_{{ .Os }}_{{ .Arch }})
	// が使うプロジェクト名。アーカイブ名 agmsg-go_<os>_<arch>.tar.gz の前半に対応する。
	projectName = "agmsg-go"
	// binaryName はアーカイブ内に格納された実行ファイル名（.goreleaser.yaml の binary）。
	binaryName = "agmsg"
)

// Options は 1 回の upgrade 実行を制御する。
type Options struct {
	// CurrentVersion は実行中バイナリに埋め込まれた version（例 "v0.0.1" / "dev"）。
	// 「既に最新」判定と要約行の表示に使う。
	CurrentVersion string
	// CheckOnly は download / 置き換えを省き、最新版の確認だけ行う。
	CheckOnly bool
	// Out は進捗・要約の出力先。未設定なら stdout。
	Out io.Writer
	// HTTPClient は HTTP クライアントを差し替える（テスト用）。
	HTTPClient *http.Client
}

type release struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Run は upgrade を実行する。
func Run(opts Options) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	token := resolveGitHubToken(os.Getenv, ghAuthToken)

	rel, err := fetchLatest(client, token)
	if err != nil {
		return fmt.Errorf("fetch latest release: %w", err)
	}
	fmt.Fprintf(opts.Out, "current: %s\nlatest:  %s\n", displayVersion(opts.CurrentVersion), rel.TagName)

	if normalize(opts.CurrentVersion) == normalize(rel.TagName) && opts.CurrentVersion != "dev" && opts.CurrentVersion != "" {
		fmt.Fprintln(opts.Out, "already at latest version")
		return nil
	}

	if opts.CheckOnly {
		fmt.Fprintln(opts.Out, "(check only — not downloading)")
		return nil
	}

	assetName := fmt.Sprintf("%s_%s_%s.tar.gz", projectName, runtime.GOOS, runtime.GOARCH)
	assetURL, checksumsURL := pickURLs(rel.Assets, assetName)
	if assetURL == "" {
		return fmt.Errorf("no asset %q in release %s", assetName, rel.TagName)
	}
	if checksumsURL == "" {
		return fmt.Errorf("no %s in release %s", checksumsName, rel.TagName)
	}

	expectedSum, err := fetchChecksum(client, checksumsURL, assetName)
	if err != nil {
		return fmt.Errorf("fetch checksum: %w", err)
	}

	binPath, err := exePath()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	destDir := filepath.Dir(binPath)

	tarballPath, err := downloadAndVerify(client, assetURL, expectedSum, destDir)
	if err != nil {
		return fmt.Errorf("download asset: %w", err)
	}
	defer os.Remove(tarballPath)

	newBin, err := extractBinary(tarballPath, destDir)
	if err != nil {
		return fmt.Errorf("extract binary: %w", err)
	}
	defer os.Remove(newBin)

	if err := os.Chmod(newBin, 0o755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(newBin, binPath); err != nil {
		return fmt.Errorf("replace binary at %s: %w", binPath, err)
	}

	fmt.Fprintf(opts.Out, "upgraded %s → %s (%s)\n", displayVersion(opts.CurrentVersion), rel.TagName, binPath)
	return nil
}

func pickURLs(assets []releaseAsset, assetName string) (string, string) {
	var assetURL, checksumsURL string
	for _, a := range assets {
		switch a.Name {
		case assetName:
			assetURL = a.BrowserDownloadURL
		case checksumsName:
			checksumsURL = a.BrowserDownloadURL
		}
	}
	return assetURL, checksumsURL
}

// exePath は symlink を解決して実体ファイルを rename 対象にする。これが無いと
// `/usr/local/bin/agmsg` のような symlink が通常ファイルで上書きされてしまう。
func exePath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func fetchLatest(client *http.Client, token string) (*release, error) {
	req, err := newGitHubRequest(releaseAPI, token)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api: %s", resp.Status)
	}
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	if r.TagName == "" {
		return nil, fmt.Errorf("empty tag_name in github api response")
	}
	return &r, nil
}

func newGitHubRequest(url, token string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token = strings.TrimSpace(token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// resolveGitHubToken は GITHUB_TOKEN を優先し、無ければ `gh auth token` にフォールバック
// する。どちらも無ければ未認証（空文字）で続行する。public repo の release 取得は
// 未認証でも通るが、token があれば rate limit が緩む。
func resolveGitHubToken(getenv func(string) string, ghToken func() (string, error)) string {
	if token := strings.TrimSpace(getenv("GITHUB_TOKEN")); token != "" {
		return token
	}
	token, err := ghToken()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(token)
}

func ghAuthToken() (string, error) {
	cmd := exec.Command("gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func fetchChecksum(client *http.Client, url, assetName string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", checksumsName, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return parseChecksum(string(body), assetName)
}

func parseChecksum(body, assetName string) (string, error) {
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", assetName)
}

func downloadAndVerify(client *http.Client, url, expectedSum, destDir string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: %s", resp.Status)
	}
	f, err := os.CreateTemp(destDir, "agmsg-dl-*.tar.gz")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != expectedSum {
		os.Remove(f.Name())
		return "", fmt.Errorf("checksum mismatch: want %s, got %s", expectedSum, got)
	}
	return f.Name(), nil
}

func extractBinary(tarball, destDir string) (string, error) {
	f, err := os.Open(tarball)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(hdr.Name) != binaryName {
			continue
		}
		out, err := os.CreateTemp(destDir, "agmsg-new-*")
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			os.Remove(out.Name())
			return "", err
		}
		if err := out.Close(); err != nil {
			os.Remove(out.Name())
			return "", err
		}
		return out.Name(), nil
	}
	return "", fmt.Errorf("%s binary not found in archive", binaryName)
}

func normalize(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

func displayVersion(v string) string {
	if v == "" {
		return "(unknown)"
	}
	return v
}
