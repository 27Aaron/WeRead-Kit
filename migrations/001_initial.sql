-- wxread 数据库基线（数据库文件仍位于 data/wxread.db）。
-- 凭据表只保留运行和 Web UI 所需的字段；令牌不会进入日志。
CREATE TABLE IF NOT EXISTS weread_account (
  alias TEXT PRIMARY KEY,
  vid TEXT NOT NULL,
  refresh_token TEXT NOT NULL,
  device_id TEXT NOT NULL,
  access_token TEXT NOT NULL DEFAULT '',
  remark TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  avatar TEXT NOT NULL DEFAULT '',
  user_vid TEXT NOT NULL DEFAULT '',
  rotated_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS weread_reading (
  alias TEXT PRIMARY KEY REFERENCES weread_account(alias) ON DELETE CASCADE,
  enabled INTEGER NOT NULL DEFAULT 0,
  book_ids TEXT NOT NULL DEFAULT '',
  minutes INTEGER NOT NULL DEFAULT 30,
  run_at TEXT NOT NULL DEFAULT '03:00',
  last_run_date TEXT NOT NULL DEFAULT '',
  last_run_at INTEGER NOT NULL DEFAULT 0,
  last_status TEXT NOT NULL DEFAULT '',
  run_book_id TEXT NOT NULL DEFAULT '',
  run_done INTEGER NOT NULL DEFAULT 0,
  run_total INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS weread_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  level TEXT NOT NULL DEFAULT 'info',
  source TEXT NOT NULL DEFAULT '',
  alias TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_weread_log_ts ON weread_log(ts DESC);
