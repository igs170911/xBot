import http.server, sys
PROJ = sys.argv[1]; RUNTIME = sys.argv[2]; PORT = int(sys.argv[3])
class H(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *a, **k): super().__init__(*a, directory=PROJ, **k)
    def translate_path(self, path):
        if path.split('?')[0].endswith('/support.js'): return RUNTIME
        return super().translate_path(path)
    def log_message(self, *a): pass
http.server.ThreadingHTTPServer(('127.0.0.1', PORT), H).serve_forever()
