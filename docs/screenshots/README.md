# Screenshots

`capture.mjs` walks every page in light and dark at 2× and writes the PNGs
here. The images are not committed: regenerate them when you need them for the
marketing site or the README.

```bash
# a throwaway stack with demo data on port 38180
cd deploy && docker compose -p argus-demo --env-file demo.env -f docker-compose.yml up -d

npm init -y && npm i playwright-core          # in a scratch directory
EXE=$(ls -d ~/.cache/ms-playwright/chromium_headless_shell-*/*/chrome-headless-shell) \
  node capture.mjs
```

Point `OUT` and the base URL in the script at your own stack before running it.

Keep the demo fleet mostly healthy with one or two real outages: a wall of red
sells nothing, and a wall of green shows none of the product.
