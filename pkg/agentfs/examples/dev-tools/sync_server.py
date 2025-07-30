import http.server
import socketserver
import tarfile
import argparse
import os
from io import BytesIO

# --- Argument Parsing ---
parser = argparse.ArgumentParser(description="LiveKit Agent Python Sync Server")
parser.add_argument("--token", required=True, help="The secret token for authorization")
parser.add_argument("--workdir", required=True, help="The working directory to unpack files into")
args = parser.parse_args()

SYNC_TOKEN = args.token
WORK_DIR = args.workdir
PORT = 8080

class SyncHandler(http.server.BaseHTTPRequestHandler):
    """A custom handler for processing file sync requests."""

    def do_PUT(self):
        if self.path != "/sync":
            self.send_response(404)
            self.end_headers()
            self.wfile.write(b"Not Found")
            return

        client_token = self.headers.get("X-LIVEKIT-AGENT-DEV-SYNC-TOKEN")
        if not client_token or client_token != SYNC_TOKEN:
            print("Unauthorized sync attempt: Invalid token.")
            self.send_response(401)
            self.end_headers()
            self.wfile.write(b"Unauthorized: Invalid Token")
            return

        try:
            content_length = int(self.headers['Content-Length'])
            fileobj = BytesIO(self.rfile.read(content_length))

            print(f"Sync request received. Unpacking to {WORK_DIR}...")
            # 'r|*' lets tarfile auto-detect compression (e.g., .tar.gz)
            with tarfile.open(fileobj=fileobj, mode='r|*') as tar:
                tar.extractall(path=WORK_DIR)

            print("Successfully unpacked tarball.")
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"Sync successful")
        except Exception as e:
            print(f"Error processing request: {e}")
            self.send_response(500)
            self.end_headers()
            self.wfile.write(f"Server Error: {e}".encode('utf-8'))

if __name__ == "__main__":
    if not os.path.isdir(WORK_DIR):
        os.makedirs(WORK_DIR)
        print(f"Created working directory: {WORK_DIR}")

    with socketserver.TCPServer(("", PORT), SyncHandler) as httpd:
        print(f"Python sync server listening on http://localhost:{PORT}")
        httpd.serve_forever()