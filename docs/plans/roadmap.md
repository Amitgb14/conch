# Roadmap

Order of upcoming work (updated 2026-09-14). Finished items move to the git
history; details for big items live in their own plan files.

## Done

1. **Plan-limit alerts** — notify once when Claude's or Codex's 5-hour,
   weekly or spend window passes 80% and 95% (configurable), per machine and
   window, remembered across TUI restarts until the window resets.
2. **Session search** — `/` in the Sessions view filters by title, agent,
   branch and ID as you type, and searches inside conversations on the
   server, showing a matching snippet.
3. **Share a session with another agent** — hand a saved conversation to a
   different agent (or a running one): conch writes the conversation to a
   handoff file in the checkout and starts the chosen agent there with a
   prompt to read it and continue.
4. **Broadcast to a group** — send one prompt to every agent in a project or
   group from the tree, and one command to its terminals. Waiting agents and
   terminals in mixed groups start unticked; the confirmation lists recipients.

## Next

5. **tmux extras** — `ctrl+b .` move tab, `ctrl+b q` split numbers,
   `ctrl+b space` layouts, clearer names in the `w` picker.
6. **Website deploy** — publish `web/`.
7. **MicroVM sandboxes** — see [microvm-sandbox.md](microvm-sandbox.md).
8. **First release** — tag v0.1.0 so `install.sh` and `conch update` have
   something to download (also unblocks section 5 of the
   [end-to-end plan](../testing/end-to-end.md)).

## Last

9. **Auto-approve rules** — per-project rules that let agents run safe
   commands without waiting for the user.

Every item ships with tests covering its edge cases (see `AGENTS.md`) and a
row in the [end-to-end plan](../testing/end-to-end.md) for what fakes can't
prove.
