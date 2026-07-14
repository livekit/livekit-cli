// Reports the installed @livekit/agents version using Node's own module
// resolution paths, so pnpm/workspace symlinks and hoisting resolve exactly as
// they will at runtime. Reads package.json directly, so it works even before
// the package is built. Prints the version to stdout, or exits non-zero if the
// package can't be found.
const { createRequire } = require('module');
const path = require('path');
const fs = require('fs');

try {
  const req = createRequire(path.join(process.cwd(), 'noop.js'));
  for (const base of req.resolve.paths('@livekit/agents') || []) {
    const pkgPath = path.join(base, '@livekit/agents', 'package.json');
    if (fs.existsSync(pkgPath)) {
      process.stdout.write(JSON.parse(fs.readFileSync(pkgPath, 'utf8')).version || '');
      process.exit(0);
    }
  }
} catch (e) {}
process.exit(1);
