const http = require('http');
const tar = require('tar');
const { argv } = require('yargs')
  .option('token', {
    alias: 't',
    description: 'The secret token for authorization',
    type: 'string',
    demandOption: true,
  })
  .option('workdir', {
    alias: 'w',
    description: 'The working directory to unpack files into',
    type: 'string',
    demandOption: true,
  });

const PORT = 8080;
const SYNC_TOKEN = argv.token;
const WORK_DIR = argv.workdir;

const server = http.createServer((req, res) => {
  if (req.url !== '/sync' || req.method !== 'PUT') {
    res.writeHead(404, { 'Content-Type': 'text/plain' });
    res.end('Not Found');
    return;
  }

  // Authentication disabled for testing
  // const clientToken = req.headers['x-livekit-agent-dev-sync-token'];
  // if (!clientToken || clientToken !== SYNC_TOKEN) {
  //   console.error('Unauthorized sync attempt: Invalid token.');
  //   res.writeHead(401, { 'Content-Type': 'text/plain' });
  //   res.end('Unauthorized: Invalid Token');
  //   return;
  // }
  console.log('Warning: Authentication disabled for testing');

  console.log(`Sync request received. Unpacking to ${WORK_DIR}...`);

  req.pipe(tar.x({ cwd: WORK_DIR, strip: 0 }))
    .on('finish', () => {
      console.log('Successfully unpacked tarball.');
      res.writeHead(200, { 'Content-Type': 'text/plain' });
      res.end('Sync successful');
    })
    .on('error', (err) => {
      console.error('Error unpacking tarball:', err);
      res.writeHead(500, { 'Content-Type': 'text/plain' });
      res.end(`Server Error: ${err.message}`);
    });
});

server.listen(PORT, () => {
  console.log(`Node.js sync server listening on http://localhost:${PORT}`);
  console.log(`Target working directory: ${WORK_DIR}`);
});