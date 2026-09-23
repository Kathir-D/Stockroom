package stockroom

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"stockroom/examples"

	"github.com/joho/godotenv"
)

// The first-run wizard. The happy path of CreateFirstAdmin needs an empty
// profiles table, which the shared test database never is; it is exercised
// end to end against a scratch Postgres instead (see the PR). Here: the gate
// that matters most, and everything after it.
func TestCreateFirstAdminRefusesOnceAnyoneExists(t *testing.T) {
	db := requireTestDB(t)
	insertTestProfile(t, db, false, "")
	_, err := db.CreateFirstAdmin(context.Background(), FirstAdminInput{
		StudentNumber: "777777", FirstName: "Late", Password: "long-enough",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateFirstAdmin with accounts present = %v, want ErrConflict", err)
	}
	if _, err := db.CreateFirstAdmin(context.Background(), FirstAdminInput{
		StudentNumber: "AB123", FirstName: "Wrong", Password: "long-enough",
	}); !errors.Is(err, ErrInvalid) {
		t.Errorf("letters under the digits default = %v, want ErrInvalid before anything else", err)
	}
}

func TestSetupProgressAndExamples(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `update app_settings set setup_step = 0, setup_completed_at = now()`)
	})

	st, err := db.SaveSetupProgress(ctx, admin, 4, false)
	if err != nil || st.Step != 4 || st.Completed {
		t.Fatalf("SaveSetupProgress = %+v, %v", st, err)
	}

	// A real student whose number happens to look like an example one must
	// survive RemoveExamples: the guard is the number AND the name.
	var realID string
	if err := db.Pool.QueryRow(ctx, `
		insert into profiles (student_number, first_name, last_name, full_name)
		values ('900004', 'Real', 'Person', 'Real Person')
		on conflict (student_number) do nothing returning id`).Scan(&realID); err != nil {
		t.Skipf("900004 is already taken in this database: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `delete from profiles where id = $1`, realID) })

	prev := db.UploadsDir
	db.UploadsDir = t.TempDir()
	t.Cleanup(func() { db.UploadsDir = prev })
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from assets where `+exampleAssetSQL)
		_, _ = db.Pool.Exec(ctx, `delete from profiles where `+exampleProfileSQL)
	})

	res, err := db.LoadExamples(ctx, admin, examples.FS)
	if err != nil || res.Assets.Failed != 0 || res.Assets.Created != 5 {
		t.Fatalf("LoadExamples = %+v, %v", res, err)
	}
	// The real 900004 means the example people are skipped, not upserted
	// over them.
	if res.People.Created+res.People.Updated != 0 || len(res.Skipped) != 1 {
		t.Errorf("examples with a real 900004 present: people %+v, skipped %q", res.People, res.Skipped)
	}
	if st, _ := db.GetSetup(ctx, admin); !st.ExamplesPresent {
		t.Error("examples loaded but GetSetup says none are present")
	}
	removed, err := db.RemoveExamples(ctx, admin)
	if err != nil || removed.Assets != 5 {
		t.Fatalf("RemoveExamples = %+v, %v", removed, err)
	}
	var stillThere bool
	_ = db.Pool.QueryRow(ctx, `select exists (select 1 from profiles where id = $1)`, realID).Scan(&stillThere)
	if !stillThere {
		t.Error("RemoveExamples deleted a real person whose number looked like an example")
	}
}

func TestConfigureFailsafeWritesTheSettingsFile(t *testing.T) {
	db := requireTestDB(t)
	ctx := context.Background()
	admin := actorFor(insertTestProfile(t, db, true, "admin-pw"))
	was := failsafeAdminConfigured()
	t.Cleanup(func() { SetFailsafeAdminConfigured(was) })

	prev := db.EnvPath
	t.Cleanup(func() { db.EnvPath = prev })
	db.EnvPath = ""
	if err := db.ConfigureFailsafe(ctx, admin, "123", "long-enough"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("no settings file = %v, want ErrNotConfigured", err)
	}

	db.EnvPath = filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(db.EnvPath, []byte("DATABASE_URL=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	number := fmt.Sprintf("8%08d", rand.IntN(100_000_000))
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `delete from profiles where student_number = $1`, number) })

	if err := db.ConfigureFailsafe(ctx, admin, admin.StudentNumber, "long-enough"); !errors.Is(err, ErrInvalid) ||
		!strings.Contains(err.Error(), "different number") {
		t.Errorf("the admin's own number = %v, want ErrInvalid", err)
	}
	if err := db.ConfigureFailsafe(ctx, admin, number, "spare $pass#word"); err != nil {
		t.Fatalf("ConfigureFailsafe: %v", err)
	}
	env, _ := godotenv.Read(db.EnvPath)
	if env["ADMIN_STUDENT_NUMBER"] != number || env["ADMIN_PASSWORD"] != "spare $pass#word" || env["DATABASE_URL"] != "x" {
		t.Errorf("settings file = %v", env)
	}
	if _, err := db.LoginByPassword(ctx, number, "spare $pass#word"); err != nil {
		t.Errorf("the spare account cannot sign in now: %v", err)
	}

	// Replacing it retires the old one: an admin with a password nothing
	// re-applies any more is a way in nobody is keeping track of.
	second := fmt.Sprintf("8%08d", rand.IntN(100_000_000))
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `delete from profiles where student_number = $1`, second) })
	if err := db.ConfigureFailsafe(ctx, admin, second, "second-spare"); err != nil {
		t.Fatalf("ConfigureFailsafe again: %v", err)
	}
	if _, err := db.LoginByPassword(ctx, number, "spare $pass#word"); err == nil {
		t.Error("the replaced failsafe can still sign in with its old password")
	}
	var stillAdmin bool
	_ = db.Pool.QueryRow(ctx, `select is_admin from profiles where student_number = $1`, number).Scan(&stillAdmin)
	if stillAdmin {
		t.Error("the replaced failsafe is still an admin")
	}

	// A retry after a failure between the file write and the retire step:
	// .env already holds the new number, and the old account must still go.
	if _, err := db.Pool.Exec(ctx, `update profiles set is_admin = true where student_number = $1`, number); err != nil {
		t.Fatal(err)
	}
	if err := db.ConfigureFailsafe(ctx, admin, second, "second-spare"); err != nil {
		t.Fatalf("ConfigureFailsafe retry: %v", err)
	}
	_ = db.Pool.QueryRow(ctx, `select is_admin from profiles where student_number = $1`, number).Scan(&stillAdmin)
	if stillAdmin {
		t.Error("a retry with the new number already in .env left the old failsafe an admin")
	}
}
