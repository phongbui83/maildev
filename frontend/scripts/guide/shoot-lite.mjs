// Screenshots for the lite guide. Usage: node shoot-lite.mjs <outdir> <locale>
import { chromium } from "playwright";
import fs from "node:fs";
const [outDir, locale = "vi"] = process.argv.slice(2);
const BASE = "https://localhost:18443";
fs.mkdirSync(outDir, { recursive: true });
const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, ignoreHTTPSErrors: true, locale: locale === "vi" ? "vi-VN" : locale === "ja" ? "ja-JP" : "en-US" });
const page = await ctx.newPage();
await page.addInitScript((loc) => { try { localStorage.setItem("maildev.locale", loc); localStorage.setItem("maildev.theme", "light"); } catch {} }, locale);
async function highlight(selectors) {
  await page.evaluate((sels) => {
    document.querySelectorAll(".__hl").forEach((n) => n.remove());
    sels.forEach((sel, i) => {
      const el = document.querySelector(sel); if (!el) return;
      const r = el.getBoundingClientRect();
      const box = document.createElement("div"); box.className = "__hl";
      Object.assign(box.style, { position: "fixed", left: r.left - 4 + "px", top: r.top - 4 + "px", width: r.width + 8 + "px", height: r.height + 8 + "px", border: "3px solid #e11d48", borderRadius: "8px", boxShadow: "0 0 0 3px rgba(225,29,72,.25)", zIndex: 99999, pointerEvents: "none" });
      const badge = document.createElement("div"); badge.className = "__hl"; badge.textContent = String(i + 1);
      Object.assign(badge.style, { position: "fixed", left: r.left - 14 + "px", top: r.top - 14 + "px", width: "24px", height: "24px", borderRadius: "12px", background: "#e11d48", color: "#fff", font: "bold 13px sans-serif", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 100000, pointerEvents: "none" });
      document.body.append(box, badge);
    });
  }, selectors);
}
async function shot(name, selectors = []) {
  await page.waitForTimeout(600); if (selectors.length) await highlight(selectors);
  await page.screenshot({ path: `${outDir}/${name}.png` });
  await page.evaluate(() => document.querySelectorAll(".__hl").forEach((n) => n.remove()));
  console.log("shot", name);
}
// 1. login + forced password change (shown with a fresh member account)
await page.goto(BASE + "/login"); await page.waitForSelector("input[autocomplete=username]");
await shot("01-login", ["form.panel", "a[href*='guide']", ".seg", "button[aria-label='Toggle theme']"]);
await page.fill("input[autocomplete=username]", "nguyenvana"); await page.fill("input[autocomplete=current-password]", "Member-Pass-2026!");
await page.click("form button[type=submit], form .btn-primary");
await page.waitForURL((u) => u.pathname.startsWith("/password"), { timeout: 20000 }); await page.waitForTimeout(600);
await shot("02-password", ["form.panel, form"]);
await page.goto(BASE + "/api/auth/logout").catch(() => {});
await ctx.clearCookies();
// 2. admin
await page.goto(BASE + "/login"); await page.waitForSelector("input[autocomplete=username]");
await page.fill("input[autocomplete=username]", "admin"); await page.fill("input[autocomplete=current-password]", "Correct-Horse-9!");
await page.click("form button[type=submit], form .btn-primary");
await page.waitForURL((u) => !u.pathname.startsWith("/login"), { timeout: 20000 });
await page.goto(BASE + "/"); await page.waitForSelector(".pcard"); await page.hover(".pcard");
await shot("03-home", [".pcard", "section .grid a:nth-child(1)", "section .grid a:nth-child(2)", "section .grid a:nth-child(4)"]);
await page.goto(BASE + "/projects/default/dev"); await page.waitForSelector(".acard");
await shot("04-dev", [".grid.md\\:grid-cols-3", "button.btn-danger", ".panel.grid nav", ".panel.grid .seg"]);
await page.click(".panel.grid nav button:nth-child(2)"); await shot("05-dev-outlook", [".panel.grid .code"]);
await page.goto(BASE + "/projects/default/inbox"); await page.waitForSelector(".cmdbar"); await page.waitForTimeout(1500);
const rows = await page.$$("div.group.cursor-pointer"); if (rows[2]) await rows[2].click(); await page.waitForTimeout(1500);
await shot("06-inbox", [".cmdbar", "div.w-full.shrink-0.flex-col.border-r", "section.hidden.min-w-0"]);
await page.goto(BASE + "/projects/default/manage"); await page.waitForSelector(".tab");
await shot("07-manage-members", ["form.mb-4", "form.mb-4 select", "table.tbl"]);
await page.click("div.mb-4.flex.border-b .tab:nth-child(2)"); await shot("08-manage-settings", ["form.panel"]);
await page.click("div.mb-4.flex.border-b .tab:nth-child(3)"); await page.waitForTimeout(800); await shot("09-manage-relay", [".panel:nth-of-type(1)"]);
await page.goto(BASE + "/admin?tab=users"); await page.waitForSelector("table.tbl");
await page.click("button.btn-primary.btn-sm"); await page.waitForTimeout(500);
await shot("10-admin-users", ["button.btn-primary.btn-sm", "form.mb-4", "table.tbl"]);
await page.goto(BASE + "/admin?tab=projects"); await page.waitForSelector("table.tbl"); await shot("11-admin-projects", ["table.tbl"]);
await page.goto(BASE + "/admin?tab=logs"); await page.waitForSelector("table.tbl"); await shot("12-admin-logs", ["select.input", "table.tbl"]);
await browser.close(); console.log("done");
