# ADR 0002: An asset belongs to at most one kit

Date: 2026-09-18. Status: accepted.

## Context

Kits (TODO Phase 8) are a named bundle of units — "Kit #1 = this camera, this lens, this bag" — added to the cart in one press. The base schema's `kit_items` is a plain join table with `primary key (kit_id, asset_id)`, so a unit may sit in any number of kits, and physically that is even true: one tripod could be listed in both the interview kit and the B-roll kit, because most days only one of them goes out.

The problem is what happens on the day both do. Nothing refuses the second checkout — a kit holds no custody, the cart is just asset ids, and `CheckOutAssets` sees a unit that is already out and refuses *that unit*. So the failure lands on a student at the shelf, holding a bag that is missing its tripod, with no way to fix it and nothing on screen explaining why the kit said it was ready. A kit whose contents are conditional on what somebody else did is not a bundle; it is a suggestion.

## Decision

An asset may be in at most one kit, enforced by a unique index on `kit_items.asset_id` (`kit_items_asset_key`, migration `20260918090000_kits_v1.sql`).

`AddAssetToKit` does not pre-check. It inserts, and on a unique violation reads the offending row back to name the kit that already holds the unit, so the refusal an admin sees is "Canon 70-200mm f/2.8 is already in \"Kit #1 — Interview\"; an item belongs to one kit" rather than a constraint name. Inserting and explaining afterwards also removes the window a check-then-insert would have had.

## Consequences

The conflict moves from checkout time, where a student cannot act on it, to kit-building time, where an admin can: buy a second one, or leave it out of this kit. The message names the other kit because that is the only fact needed to resolve it.

Two kits that genuinely want to share a tripod cannot both list it. That is the intended trade, and the alternative — showing "also in Kit #2" everywhere and letting the collision happen — is a warning nobody reads at the moment it matters.

The rule lives in the database, not only in Go, for the same reason the asset-tag default does (CLAUDE.md §13, 2026-09-14): every writer is held to it, including `seed.sql`, a hand-written `INSERT` in Studio, and a restore. Relaxing it later is dropping one index plus the branch in `explainKitInsert`; the UI would then need somewhere to say "this unit is in two kits", which is the work the rule avoids.
