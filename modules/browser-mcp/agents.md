### Browser checks (browser-mcp module)

Check pages with the `playwright` MCP server, which drives its own headless Chromium; never the owner's browser. It starts at 1280×900; resize for narrow layouts. In a lane, visit the lane's port. Snapshots and screenshots land in `.playwright-mcp/`, which is gitignored.
