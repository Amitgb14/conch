#!/bin/bash
# Stage the import demo's scratch HOME: a made-up shop's repository with the
# two worktrees Conductor made for it, and a made-up Conductor database
# whose conversations ran there — plus one in a marketing site whose
# repository is gone. Nothing here reads your own Conductor, conch or home.
#
#   stage.sh HOME
set -euo pipefail
T=$(cd "$(dirname "$0")" && pwd)
H=${1:?usage: stage.sh HOME}
rm -rf "$H"
mkdir -p "$H/src" "$H/conductor/workspaces/storefront" "$H/.config/conch"

cat > "$H/.config/conch/config.toml" <<'EOF'
[notify]
enabled = false
desktop = false
[update]
check_releases = false
EOF

R=$H/src/storefront
mkdir -p "$R/app/checkout" "$R/app/cart" "$R/components"
cd "$R"
git init -q -b main
git config user.name "Ada Lovelace"; git config user.email ada@example.com
printf '{\n  "name": "storefront",\n  "private": true,\n  "scripts": { "dev": "next dev", "test": "vitest" }\n}\n' > package.json
printf '# storefront\n\nA small shop: products, a cart and checkout.\n' > README.md
printf 'export function total(cart) {\n  return cart.items.reduce((n, i) => n + i.price * i.qty, 0)\n}\n' > app/checkout/total.ts
printf 'export default function CartPage() {}\n' > app/cart/page.tsx
printf 'export function CartBadge() {}\n' > components/header.tsx
git add -A && git commit -qm "Products, cart and checkout"
echo "node_modules" > .gitignore && git add -A && git commit -qm "Ignore node_modules"

# Conductor's workspaces are git worktrees of the repository, named after cities.
git worktree add -q -b ada/coupon-codes "$H/conductor/workspaces/storefront/lisbon"
git worktree add -q -b ada/cart-badge "$H/conductor/workspaces/storefront/kyoto"
( cd "$H/conductor/workspaces/storefront/lisbon" &&
  printf 'export function applyCoupon(cart, code) {}\n' > app/checkout/coupon.ts &&
  git add -A && git commit -qm "applyCoupon" )
( cd "$H/conductor/workspaces/storefront/kyoto" &&
  printf "'use client'\nexport function CartBadge() {}\n" > components/header.tsx &&
  git commit -qam "Optimistic cart count" )

# The marketing site's repository and worktree are deliberately not made:
# its conversation is the one whose repository is gone.
python3 "$T/fakeconductor.py" "$H" >/dev/null
echo "staged $H"
