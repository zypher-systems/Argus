import { chromium } from "playwright-core";
const OUT = "/home/zypher/workspace/argus/docs/screenshots";
const b = await chromium.launch({ executablePath: process.env.EXE });
const pages = [["overview","/"],["monitors","/monitors"],["monitor-detail","/monitors/1"],["environments","/environments"],["incidents","/incidents"],["settings","/settings"],["new-monitor","/monitors/new"]];
for (const theme of ["light","dark"]) {
  const ctx = await b.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2 });
  const p = await ctx.newPage();
  await p.addInitScript((t) => localStorage.setItem("argus-theme", t), theme);
  await p.goto("http://localhost:38180/login");
  await p.waitForTimeout(700);
  await p.screenshot({ path: `${OUT}/login-${theme}.png` });
  await p.fill('input[type=email]', "admin@example.com");
  await p.fill('input[type=password]', "change-me-too");
  await p.click('button[type=submit]');
  await p.waitForURL(u => !u.pathname.startsWith("/login"));
  for (const [name, path] of pages) {
    await p.goto("http://localhost:38180" + path);
    await p.waitForTimeout(1800);
    await p.screenshot({ path: `${OUT}/${name}-${theme}.png` });
  }
  await p.goto("http://localhost:38180/status/zypher");
  await p.waitForTimeout(1400);
  await p.screenshot({ path: `${OUT}/status-page-${theme}.png`, fullPage: true });
  await ctx.close();
}
await b.close();
