# ADR 0003: The activity log is append-only and survives a restore

Date: 2026-09-26. Status: accepted.

## Context

ROADMAP 2.4 asks for one log of every action at the closet so that a missing item can be traced, and says two things about it: rows cannot be edited or deleted through the app, and a restore does not erase rows written after the backup was taken.

The base schema's `activity_log` was the opposite on both counts. Deleting an asset cascaded its rows away, and a restore truncates every table and reloads the archive, so restoring last night's backup deleted everything logged today. That is the log somebody with something to hide would most want gone, and the restore is a button in the admin panel.

## Decision

Triggers on `activity_log` refuse `UPDATE`, `DELETE` and `TRUNCATE` for every role, the superuser included (migration `20260926090000`). The foreign keys to `assets` and `profiles` are dropped, and each row snapshots the actor's name, the item's serial and a readable summary when it is written.

The restore is the one path that still truncates the table, because it runs under `session_replication_role = replica`, where ordinary triggers do not fire. Before the truncate it copies the live `activity_log` and `closet_visits` aside. After the load it inserts back every row whose `id` the archive lacks, and writes a row recording the restore. All of this happens in one transaction.

## Consequences

Nothing in the application can remove a log row, and restoring an old backup adds to the log instead of rewinding it. `RestoreResult.kept_newer` says how many rows were kept.

A person with shell access and the database password can still disable the trigger. That is outside what the app can defend, and the same access already allows anything.

Tests can no longer clean up after themselves with `delete from activity_log`. They scope their assertions by actor and time instead, and the development database keeps every row they write.

The log grows forever. That is the owner's choice (2026-09-26, "logs forever"). Pruning later means one function that runs with the triggers disabled, which this ADR would then need to cover.
