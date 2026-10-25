# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Restaurant staff across roles: waiters, cashiers, chefs, branch managers, HQ managers, and customers using the QR menu. Staff need one reliable place to complete branch operations, from opening and updating orders through kitchen preparation, payment, inventory, reporting, and audit review.

## Product Purpose

SofraPOS runs the operational workflows of an eight-branch restaurant chain. It supports table service, kitchen display operations, cashier and split-bill flows, recipe-based inventory and waste tracking, campaign handling, branch and HQ reporting, QR menu ordering, and infrastructure diagnostics. Success means staff can complete these workflows with consistent totals, current operational state, and traceable changes.

## Positioning

SofraPOS is a single operational system for the whole restaurant chain, connecting branch service, kitchen, cashier, inventory, and HQ oversight in one role-aware product.

## Operating Context

The product is used across restaurant branches during service and at close of shift. Waiters manage tables and orders; kitchen staff work from station-filtered KDS queues; cashiers apply campaigns, split bills, accept payments, and produce Z reports; branch managers handle stock, waste, counts, and branch operations; HQ managers review cross-branch performance and append-only audit logs; customers browse the digital QR menu with variants and modifiers.

## Capabilities and Constraints

- Role-based access controls which tabs and operations each user can access.
- Order changes use optimistic locking, row locking, and append-only order events so totals and state can be rebuilt and audited.
- Monetary values are integer Turkish kuruş; split payments and discount allocation must preserve exact totals.
- KDS supports cold, hot, and bar stations, course order, delayed-item indicators, and event replay after a disconnected client reconnects.
- Inventory resolves product recipes, semi-finished sub-recipes, variants, modifiers, waste, and physical-count variance.
- Campaigns compare stackable and non-stackable rules and retain an explanation of the selected result.
- The current interface and product terminology are Turkish.
- The current implementation is Svelte 5 / SvelteKit with a Go Fiber API and PostgreSQL 16; the existing codebase is the source of truth for these stack constraints.

## Brand Commitments

- Product name: SofraPOS.
- Preserve Turkish terminology in user-facing workflows unless a later product decision changes it.
- Preserve role-aware access and clear separation of waiter, cashier, chef, branch manager, HQ manager, and customer contexts.
- Preserve auditability and explainable operational state, including order events, campaign explanations, and audit logs.

## Evidence on Hand

- `README.md` documents the product scope, demo roles, workflows, API surfaces, and deterministic verification setup.
- `frontend/src/routes/+page.svelte` contains the current operational interface for tables, KDS, cashier, inventory, HQ, QR menu, and infrastructure diagnostics.
- `backend/internal/domain/` and `backend/internal/server/` contain the implemented order, split-bill, KDS, recipe, campaign, inventory, auth, reporting, and audit behavior.
- `TESTING.md`, `requests.http`, and `tests/` contain live verification and load-test evidence.
- No external testimonials, customer case studies, or marketing proof assets were identified in the repository; future work must not invent them.

## Product Principles

- Give every restaurant role a dependable path through its real operating task.
- Keep branch work and HQ oversight connected through shared, current state.
- Make financial and inventory outcomes exact and explainable.
- Preserve traceability when people or systems change an order.

