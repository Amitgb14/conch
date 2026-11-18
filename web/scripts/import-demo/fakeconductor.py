#!/usr/bin/env python3
"""Write a made-up Conductor database for the import demo.

Everything in it is invented — a small shop called storefront and a
marketing site — because the demo is published: it must never be built
from anybody's real Conductor data. The tables and columns are the ones
conch reads (internal/sessions/conductor.go; conductor_test.go makes the
same shape): repos, workspaces, sessions and session_messages, an
assistant row being one message of the agent's stream as JSON.

  fakeconductor.py HOME

writes HOME/Library/Application Support/com.conductor.app/conductor.db
with paths under HOME, which the caller has made (or, for the marketing
site, deliberately not made: its repository is gone).
"""
import json
import os
import sqlite3
import sys


def claude(*parts):
    """One assistant message of Claude's stream: text, and tool calls as
    ("tool", name, input), which the import leaves out."""
    content = []
    for p in parts:
        if isinstance(p, tuple):
            content.append({"type": "tool_use", "id": "toolu_demo", "name": p[1], "input": p[2]})
        else:
            content.append({"type": "text", "text": p})
    return json.dumps({"type": "assistant", "message": {"role": "assistant", "content": content}})


def tool_result(text):
    return json.dumps({"type": "user", "message": {"role": "user", "content": [{"type": "tool_result", "content": text}]}})


def codex(text):
    return json.dumps({"type": "item.completed", "item": {"type": "agent_message", "text": text}})


def main(home):
    src = os.path.join(home, "src")
    ws = os.path.join(home, "conductor", "workspaces")
    repos = [
        ("r-storefront", "storefront", os.path.join(src, "storefront")),
        # Deleted after Conductor worked in it: nothing of it is on disk.
        ("r-marketing", "marketing-site", os.path.join(src, "marketing-site")),
    ]
    workspaces = [
        ("w-lisbon", "r-storefront", "lisbon", "ada/coupon-codes", os.path.join(ws, "storefront", "lisbon")),
        ("w-kyoto", "r-storefront", "kyoto", "ada/cart-badge", os.path.join(ws, "storefront", "kyoto")),
        ("w-paris", "r-marketing", "paris", "ada/pricing-copy", os.path.join(ws, "marketing-site", "paris")),
    ]
    # id, workspace, title, agent, created, updated, messages
    sessions = [
        ("0199f1c2-3a10-7b4e-9c21-4f8a2d6e1b01", "w-lisbon", "Add coupon codes to checkout", "claude",
         "2026-11-02 10:14:00", "2026-11-02 11:02:00", [
             ("user", "Add coupon codes to checkout. A code takes a percentage or a fixed amount off the cart; "
                      "codes have an expiry date and a usage limit. Store them in Postgres next to orders."),
             ("assistant", claude("I'll look at how checkout totals are computed first.",
                                  ("tool", "Read", {"file_path": "app/checkout/total.ts"}))),
             ("assistant", tool_result("export function total(cart: Cart) { … }")),
             ("assistant", claude("Plan:\n\n1. A `coupons` table (code, kind, amount, expires_at, max_uses, uses).\n"
                                  "2. `applyCoupon(cart, code)` in `app/checkout/coupon.ts`, returning the discount or why it was refused.\n"
                                  "3. A field on the checkout page, and the discount as a line in the summary.\n\n"
                                  "I've written the migration and `applyCoupon`; the page is next.",
                                  ("tool", "Write", {"file_path": "db/migrations/0007_coupons.sql"}))),
             ("user", "Fixed-amount coupons must never take the total below zero. And show the reason when a code is refused."),
             ("assistant", claude("Done: the discount is capped at the cart's subtotal, and a refused code says why under the field "
                                  "(*expired*, *used up*, *not a code*). Still to do: an admin page to create codes — "
                                  "for now they're inserted with `pnpm coupons:add`."),),
         ]),
        ("0199f1c2-9e44-7f02-8a6b-1c3d5e7f9a02", "w-lisbon", "Tests for the coupon validator", "codex",
         "2026-11-02 11:20:00", "2026-11-02 11:41:00", [
             ("user", "Write tests for applyCoupon in app/checkout/coupon.ts: expired, used up, unknown code, "
                      "percentage, fixed amount bigger than the cart."),
             ("assistant", codex("Added app/checkout/coupon.test.ts with 9 cases. All pass except one: a code is matched "
                                 "case-sensitively, so `SPRING10` and `spring10` differ. Should codes be case-insensitive?")),
             ("user", "Yes, case-insensitive. Normalise to upper case when storing."),
             ("assistant", codex("Codes are upper-cased on insert and on lookup; the migration upper-cases existing rows. 10 tests, all passing.")),
         ]),
        ("0199f4a8-0b17-7c3d-b5e9-2a4c6e8f0d03", "w-kyoto", "Cart badge keeps the old count", "claude",
         "2026-11-05 16:03:00", "2026-11-05 16:40:00", [
             ("user", "The cart badge in the header still shows 3 after I remove an item on the cart page. It's right after a reload."),
             ("assistant", claude("The header reads the count from `useCart()`, but the cart page removes items with a server action "
                                  "and never revalidates the layout, so the header keeps its first render.",
                                  ("tool", "Grep", {"pattern": "useCart"}))),
             ("assistant", claude("Fixed by calling `revalidatePath('/', 'layout')` after removing an item, and added a Playwright "
                                  "test that removes an item and checks the badge.")),
             ("user", "The badge flickers to 0 for a moment now. Can it update optimistically?"),
             ("assistant", claude("It now uses `useOptimistic` for the count: it drops at once, and the server's answer "
                                  "replaces it. No flicker in the test recording. Not yet done: the same for *add to cart* "
                                  "on the product page.")),
         ]),
        ("0199f4a8-7d52-7a19-8e0c-6b8d0f2a4c04", "w-kyoto", "Untitled", "claude",
         "2026-11-06 09:12:00", "2026-11-06 09:30:00", [
             ("user", "The product grid squeezes to one narrow column on small phones. Two columns below 400px, please."),
             ("assistant", claude("Changed the grid to `grid-cols-2` below the `sm` breakpoint and let the cards' images "
                                  "shrink with `aspect-square`. Checked at 360px and 390px.")),
         ]),
        ("0199f4a8-c1e3-7b66-9d4f-8e0a2c4e6a05", "w-kyoto", "Untitled", "claude",
         "2026-11-06 14:00:00", "2026-11-06 14:00:00", []),  # opened, nothing said: not imported
        ("0199f7b0-5a2c-7e81-a3d7-0c2e4a6c8e06", "w-paris", "Rewrite the pricing page copy", "claude",
         "2026-11-09 13:45:00", "2026-11-09 14:20:00", [
             ("user", "Rewrite the pricing page copy: three plans, plain words, no exclamation marks. Keep the FAQ."),
             ("assistant", claude("Rewrote `content/pricing.mdx`: Starter, Team and Business, each with one sentence on who it is for. "
                                  "The FAQ is unchanged except the refund answer, which now matches the terms page.")),
             ("user", "Good. Make Team the highlighted plan."),
             ("assistant", claude("Team is highlighted and listed in the middle on wide screens, first on phones.")),
         ]),
    ]

    db = os.path.join(home, "Library", "Application Support", "com.conductor.app", "conductor.db")
    os.makedirs(os.path.dirname(db), exist_ok=True)
    if os.path.exists(db):
        os.remove(db)
    con = sqlite3.connect(db)
    con.executescript("""
CREATE TABLE repos (id TEXT PRIMARY KEY, name TEXT, root_path TEXT);
CREATE TABLE workspaces (id TEXT PRIMARY KEY, repository_id TEXT, directory_name TEXT, branch TEXT, workspace_path TEXT);
CREATE TABLE sessions (id TEXT PRIMARY KEY, claude_session_id TEXT, created_at TEXT, updated_at TEXT,
  workspace_id TEXT, title TEXT DEFAULT 'Untitled', agent_type TEXT);
CREATE TABLE session_messages (id TEXT PRIMARY KEY, session_id TEXT, role TEXT, content TEXT, created_at TEXT);
""")
    con.executemany("INSERT INTO repos VALUES (?,?,?)", repos)
    con.executemany("INSERT INTO workspaces VALUES (?,?,?,?,?)", workspaces)
    n = 0
    for sid, wid, title, agent, created, updated, msgs in sessions:
        con.execute("INSERT INTO sessions VALUES (?,?,?,?,?,?,?)", (sid, None, created, updated, wid, title, agent))
        hh, mm = int(created[11:13]), int(created[14:16])
        for i, (role, content) in enumerate(msgs):
            n += 1
            m = mm + 2 * i
            at = "%s %02d:%02d:00" % (created[:10], hh + m // 60, m % 60)
            con.execute("INSERT INTO session_messages VALUES (?,?,?,?,?)", ("m%d" % n, sid, role, content, at))
    con.commit()
    con.close()
    print(db)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    main(sys.argv[1])
