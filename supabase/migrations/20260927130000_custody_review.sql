-- Returns an admin should look at, lost items, and how each return happened
-- (CLAUDE.md §7).

alter table custody_events
  -- How the item came back. scan = its barcode at scanner speed; typed = a
  -- serial typed into the scan path; button = a Check in button; kit = a
  -- whole-kit return; lost = an admin marked it lost. Null while it is out
  -- and for returns written before this was recorded.
  add column returned_via text
    check (returned_via in ('scan', 'typed', 'button', 'kit', 'lost')),
  -- returned, or lost: an admin closed the event without the item.
  add column outcome text not null default 'returned'
    check (outcome in ('returned', 'lost')),
  -- Why an admin should look at this return: 'damage' (a damage note was
  -- left) and 'not_scanned' (a student returned it without scanning it, so
  -- nothing shows the item came back). Empty means nothing to review.
  add column review_reasons text[] not null default '{}',
  -- When an admin cleared it, and who. No foreign key, like activity_log: a
  -- review must not stop an account being archived or deleted.
  add column reviewed_at timestamptz,
  add column reviewed_by uuid;

create index idx_custody_needs_review on custody_events (checked_in_at desc)
  where reviewed_at is null and review_reasons <> '{}';

-- When a loan is due: the closing time on the next school day after the last
-- day of use the borrower picks (CLAUDE.md §7, decided 2026-09-26). 'HH:MM',
-- 24-hour, in this machine's time zone.
alter table app_settings
  add column due_time text not null default '15:30'
    check (due_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$');
