import { createClient } from '@supabase/supabase-js';

// Local Supabase stack (see `supabase status`). Single trusted workstation,
// so the app uses the service_role key directly and skips RLS entirely.
// Role permission checks happen in app code instead.
const SUPABASE_URL = 'http://127.0.0.1:54321';
const SUPABASE_SERVICE_ROLE_KEY =
  'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6InNlcnZpY2Vfcm9sZSIsImV4cCI6MTk4MzgxMjk5Nn0.EGIM96RAZx35lJzdJsyH-qQwv8Hdp7fsn3W0YpN81IU';

export const supabase = createClient(SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY);
