-- The rules that were constants become admin settings (ROADMAP A4): how far
-- ahead the last day of use may be, whether an overdue item blocks checkout,
-- the session idle timeout, and the scanner's keystroke threshold. And the
-- school's closed dates, which the due date skips like a weekend.
alter table app_settings
  add column max_checkout_days integer not null default 7
    constraint app_settings_max_checkout_days check (max_checkout_days between 1 and 60),
  add column overdue_blocks_checkout boolean not null default true,
  -- Null means SESSION_IDLE_MINUTES from the config, 10 unless set there.
  add column session_idle_minutes integer
    constraint app_settings_session_idle_minutes check (session_idle_minutes between 1 and 240),
  add column scan_threshold_ms integer not null default 50
    constraint app_settings_scan_threshold_ms check (scan_threshold_ms between 10 and 200),
  add column closed_dates date[] not null default '{}'
    constraint app_settings_closed_dates check (cardinality(closed_dates) <= 400);
