// node --test uitest/ — run by TestUILogic.
import { test } from "node:test"
import assert from "node:assert/strict"
import { agentIcon, icon, escapeAttr, agentColors } from "../ui/icons.mjs"

test("agentIcon", () => {
  for (const a of Object.keys(agentColors)) {
    const s = agentIcon(a, 22)
    assert.match(s, new RegExp(`color:${agentColors[a]}`))
    assert.match(s, /width:22px;height:22px/)
    assert.match(s, /<svg width="14"/)
  }
  // An agent conch has no mark for gets the default, not a blank.
  assert.match(agentIcon("someones-own", 20), /<rect x="6" y="6"/)
  assert.match(agentIcon("shell"), /var\(--muted\)/)
  // A name from a program cannot break out of the attribute.
  assert.doesNotMatch(agentIcon('x"><img src=y>'), /<img/)
})

test("icon and escapeAttr", () => {
  assert.match(icon("plus", 18), /width="18"/)
  assert.match(icon("nonsense"), /<svg[^>]*><\/svg>/) // unknown: empty, not an error
  assert.equal(escapeAttr(`<a href="x">'&`), "&lt;a href=&quot;x&quot;&gt;&#39;&amp;")
  assert.equal(escapeAttr(null), "")
  assert.equal(escapeAttr(3), "3")
})
