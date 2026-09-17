package stockroom

import (
	"context"
	"log"
	"time"
)

// The nightly schedule, in-process (docs/design/backup.md §E.6).
//
// A goroutine in the Go server rather than Task Scheduler or launchd, for
// three reasons that all turned out to matter:
//
//   - It runs on both operating systems with one implementation, where OS
//     scheduling is two, one of which nobody on this project can test.
//   - It sidesteps findDotEnv (config.go), which walks up from the working
//     directory -- and a Windows scheduled task starts in System32, which is
//     not inside the repository.
//   - It can catch up. The closet PC is "always on" in the sense that a school
//     machine is always on, which is to say it gets unplugged. A task that
//     fires at 02:00 and finds the machine off simply does not run; a
//     goroutine that starts with the server can notice the last run is stale
//     and back up immediately, which is the whole of "back up first thing when
//     the machine is available".
//
// The accepted limit is that it does not run when the server does not. If the
// server is down nobody is using Stockroom either, and the boot catch-up
// covers the gap.

// schedulerTick is how often the loop wakes to ask whether it is time. A
// minute is fine: the schedule is an hour of the day, and settings are
// re-read on every tick so changing the hour in the panel takes effect without
// a restart.
const schedulerTick = time.Minute

// StartBackupScheduler runs the nightly backup until ctx is cancelled. It
// returns immediately; the work happens on its own goroutine.
func (db *DB) StartBackupScheduler(ctx context.Context) {
	go db.runScheduler(ctx)
}

func (db *DB) runScheduler(ctx context.Context) {
	// A short pause before the boot catch-up so start-up logging is not
	// interleaved with a backup, and so a server restarted three times in a
	// minute does not start three of them (the advisory lock would refuse the
	// second and third anyway, but not making the noise is better).
	select {
	case <-ctx.Done():
		return
	case <-time.After(10 * time.Second):
	}

	if due, why := db.catchUpDue(ctx); due {
		log.Printf("backup: %s; running now", why)
		db.runScheduled(ctx, BackupSourceCatchUp)
	}

	ticker := time.NewTicker(schedulerTick)
	defer ticker.Stop()

	// lastRun is the day a scheduled run last fired, so crossing the hour
	// cannot fire sixty times. It is a date rather than a timestamp because
	// the schedule is "once a day at this hour".
	lastRun := ""
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			settings, err := db.loadSettings(ctx)
			if err != nil {
				continue
			}
			today := now.Format("2006-01-02")
			if now.Hour() != settings.ScheduleHour || lastRun == today {
				continue
			}
			lastRun = today
			db.runScheduled(ctx, BackupSourceScheduled)
		}
	}
}

// runScheduled performs one run and logs the outcome. It never propagates an
// error, because there is nobody to propagate one to: the log and the backup
// screen's status are how a failed nightly run becomes visible.
func (db *DB) runScheduled(ctx context.Context, source string) {
	// Its own deadline, so a target that hangs cannot leave the goroutine
	// stuck past the next night.
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()

	res, err := db.RunBackup(runCtx, source)
	switch {
	case err != nil:
		log.Printf("backup (%s) FAILED: %v", source, err)
	case res.Skipped:
		log.Printf("backup (%s) skipped: another backup is already running", source)
	default:
		log.Printf("backup (%s) ok: %d rows across %d tables into %s",
			source, res.Rows, len(res.Tables), res.Dir)
		for _, t := range res.Targets {
			if !t.OK {
				log.Printf("backup (%s): %s FAILED: %s", source, t.Target, oneLine(t.Error))
			}
		}
	}
}

// catchUpDue reports whether the last successful run is stale enough to run
// one now, and says why in words the log can print.
//
// A machine that has never backed up is due immediately. That is deliberate:
// the first night after setup is the one where there is nothing to fall back
// on, and waiting until 2 a.m. to find out that the folder is unwritable is
// waiting eighteen hours for a message somebody could have had at set-up time.
func (db *DB) catchUpDue(ctx context.Context) (bool, string) {
	settings, err := db.loadSettings(ctx)
	if err != nil {
		return false, ""
	}
	dir := db.BackupDir
	if dir == "" {
		dir = settings.BackupDir
	}
	if dir == "" {
		return false, ""
	}
	state := readBackupState(dir)
	local := state.Targets[localTarget]
	if local.LastSuccess == nil {
		return true, "no backup has ever completed on this machine"
	}
	age := time.Since(*local.LastSuccess)
	if age > time.Duration(settings.StaleHours)*time.Hour {
		return true, "the last backup was " + staleAge(ptrFloat(age.Hours())) + " ago"
	}
	return false, ""
}

func ptrFloat(f float64) *float64 { return &f }
