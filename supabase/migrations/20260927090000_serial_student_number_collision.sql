-- A barcode is either an item or a card, never both (ROADMAP §3.1).
--
-- A card scanned while somebody else is signed in now switches accounts, so
-- the app has to know which kind of code it read. The rule that makes that a
-- lookup rather than a guess: no asset's serial may equal an account's
-- student number, ignoring case. Checked by triggers on both tables rather
-- than in Go, so the admin panel, the CSV imports, seed.sql and a hand-written
-- INSERT are all held to it. Existing rows are not checked, so an install that
-- already has a collision still upgrades; the next write to either row is
-- refused and names the other one.

create or replace function refuse_serial_student_number_collision()
returns trigger language plpgsql as $$
declare
  clash text;
begin
  if tg_table_name = 'assets' then
    if new.serial_number is null then
      return new;
    end if;
    select coalesce(nullif(trim(coalesce(first_name, '') || ' ' || coalesce(last_name, '')), ''), full_name, 'an account')
      into clash
      from profiles where lower(student_number) = lower(new.serial_number) limit 1;
    if found then
      raise exception 'serial number % is the student number of %, and a scan could not tell them apart', new.serial_number, clash
        using errcode = '23505', constraint = 'serial_not_a_student_number';
    end if;
  else
    if new.student_number is null then
      return new;
    end if;
    select name into clash
      from assets where lower(serial_number) = lower(new.student_number) limit 1;
    if found then
      raise exception 'student number % is the serial number of %, and a scan could not tell them apart', new.student_number, clash
        using errcode = '23505', constraint = 'student_number_not_a_serial';
    end if;
  end if;
  return new;
end;
$$;

create trigger trg_assets_serial_not_student_number
  before insert or update of serial_number on assets
  for each row execute function refuse_serial_student_number_collision();

create trigger trg_profiles_student_number_not_serial
  before insert or update of student_number on profiles
  for each row execute function refuse_serial_student_number_collision();

create index if not exists idx_profiles_student_number_lower on profiles (lower(student_number));
create index if not exists idx_assets_serial_lower on assets (lower(serial_number));
