# browser-mcp

> A Playwright MCP server, so agents check pages in their own headless Chromium instead of driving the owner's browser.

**Status:** working, Claude Code only (`.mcp.json`). **Origin:** fabworkspace `.mcp.json`, added after
agents driving the owner's real browser through an extension fought with per-site zoom and window
tiling when taking screenshots and resizing.

- Runs on the host, not in a container: it only visits published ports and runs no project code.
- `--isolated` gives each session an in-memory profile, so parallel sessions don't share cookies.
  Logging in as two different users in two lanes works because each lane has its own host port.
- Output lands in `.playwright-mcp/` in the session's directory (gitignored).
- Requires Node and a Chromium. Set `--executable-path` in `.mcp.json` if Chromium isn't where
  Playwright looks. The package version is pinned; bump it deliberately.
