"""Synthetic demand only; no Elasticsearch indexing workload is generated."""
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

VALUE = Path('/tmp/backlog')


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != '/metrics':
            self.send_error(404)
            return
        value = float(VALUE.read_text()) if VALUE.exists() else 0
        body = f'# TYPE lab_backlog gauge\nlab_backlog {value}\n'.encode()
        self.send_response(200)
        self.send_header('Content-Type', 'text/plain; version=0.0.4')
        self.end_headers()
        self.wfile.write(body)


if __name__ == '__main__':
    HTTPServer(('0.0.0.0', 8080), Handler).serve_forever()
