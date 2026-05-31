package store

// schemaSQL はオリジナル fujibee/agmsg と互換の messages スキーマ
// (design.md §5.1)。同一 messages.db を bash 版・Go 版で読めることを狙う。
//
//   - id           単調増加。watch の watermark に使う。
//   - read_at      NULL = 未読。inbox が埋める。
//   - created_at   未指定なら SQLite 側で ISO-8601 (UTC) を補う。
//
// IF NOT EXISTS で冪等にし、既存 DB（bash 版が作成したもの含む）にも安全に適用する。
const schemaSQL = `
CREATE TABLE IF NOT EXISTS messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  team TEXT NOT NULL,
  from_agent TEXT NOT NULL,
  to_agent TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  read_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_unread  ON messages(team, to_agent, read_at);
CREATE INDEX IF NOT EXISTS idx_history ON messages(team, created_at);
`
