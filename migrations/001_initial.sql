-- 数据库基线

CREATE TABLE IF NOT EXISTS weread_account (
  vid TEXT PRIMARY KEY,
  refresh_token TEXT NOT NULL,
  device_id TEXT NOT NULL,
  access_token TEXT NOT NULL DEFAULT '',
  remark TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  avatar TEXT NOT NULL DEFAULT '',
  user_vid TEXT NOT NULL DEFAULT '',
  profile TEXT NOT NULL DEFAULT '',
  card TEXT NOT NULL DEFAULT '',
  shelf TEXT NOT NULL DEFAULT '',
  rotated_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  details_cached_at INTEGER NOT NULL DEFAULT 0,
  profile_updated_at INTEGER NOT NULL DEFAULT 0
);

-- 阅读配置:每个账号一行,断点字段用于中断后续跑
CREATE TABLE IF NOT EXISTS weread_reading (
  vid TEXT PRIMARY KEY REFERENCES weread_account(vid) ON DELETE CASCADE,
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

-- 运行日志:滚动保留 30 天 / 2 万条
CREATE TABLE IF NOT EXISTS weread_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  level TEXT NOT NULL DEFAULT 'info',
  source TEXT NOT NULL DEFAULT '',
  vid TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_weread_log_ts ON weread_log (ts DESC);

-- 推送渠道:每个渠道一行(type ∈ bark / telegram / serverchan / pushplus)
CREATE TABLE IF NOT EXISTS weread_push_channel (
  type TEXT PRIMARY KEY,
  enabled INTEGER NOT NULL DEFAULT 0,
  params TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL DEFAULT 0
);
