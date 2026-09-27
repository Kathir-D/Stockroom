-- The serial / student-number trigger (20260927090000) under concurrency.
--
-- Two transactions, one inserting an asset with serial 123456 and one a
-- profile with student number 123456, each looked for the other's row before
-- either had committed, found nothing, and both went in. A transaction-scoped
-- advisory lock on the lowercased code makes the second wait for the first,
-- and its lookup then sees the committed row. The lock is keyed by the code,
-- so unrelated writes never wait on each other.

create or replace function refuse_serial_student_number_collision()
returns trigger language plpgsql as $$
declare
  clash text;
  code text;
begin
  if tg_table_name = 'assets' then
    code := new.serial_number;
  else
    code := new.student_number;
  end if;
  if code is null then
    return new;
  end if;
  perform pg_advisory_xact_lock(hashtext('stockroom-scan-code:' || lower(code)));

  if tg_table_name = 'assets' then
    select coalesce(nullif(trim(coalesce(first_name, '') || ' ' || coalesce(last_name, '')), ''), full_name, 'an account')
      into clash
      from profiles where lower(student_number) = lower(code) limit 1;
    if found then
      raise exception 'serial number % is the student number of %, and a scan could not tell them apart', code, clash
        using errcode = '23505', constraint = 'serial_not_a_student_number';
    end if;
  else
    select name into clash
      from assets where lower(serial_number) = lower(code) limit 1;
    if found then
      raise exception 'student number % is the serial number of %, and a scan could not tell them apart', code, clash
        using errcode = '23505', constraint = 'student_number_not_a_serial';
    end if;
  end if;
  return new;
end;
$$;
