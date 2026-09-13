# ADR 0001: An asset may file under any category node

Date: 2026-09-13. Status: accepted.

## Context

The category tree is three levels deep, Type → Category → Model, and every seeded unit hangs off a Model. The admin panel offers Models when filing a unit. CLAUDE.md §6.2 describes the tree that way, and it would be natural to have `CreateAsset` and `UpdateAsset` refuse a `category_id` that is not at depth 3.

Two things argued against the rule. A Type with no Categories under it yet (the seed ships `Primes` as a Category with no Models, and a freshly created Type has nothing) would have nowhere to put a unit at all until the admin builds the branch down. And the browse list already handles a short path: `category_path` is whatever the node's ancestry is, and the sort key compares level by level, so a unit at depth 1 or 2 simply sorts among its siblings' branches.

## Decision

`requireCategory` checks only that the node exists. Depth is not checked. An asset may point at any category row.

## Consequences

An admin can file a unit under a Type or a Category, and it lists and sorts correctly. The UI should still default to offering Models, because that is where nearly every unit belongs, but the server does not enforce it. The `MaxCategoryDepth` rule in `categories_admin.go` is unaffected: it bounds how deep the tree goes, not where assets attach.

If a depth rule is wanted later, `requireCategory` already has the tree in hand (`tree.depth[id]`), so it is a one-line change plus a message.
