# Server-rendered templates

Empty. The console shell in `console/static/` is a static document that fetches
its data from the API; no server-side rendering exists yet.

This directory is reserved for `html/template` files if server-rendered pages
are added — most likely the login page, where rendering server-side avoids a
flash of unauthenticated content.

If that never happens, this directory should be deleted rather than kept as a
gesture.
