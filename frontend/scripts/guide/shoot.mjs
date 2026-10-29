// Screenshots for the user guide. Usage: node shoot.mjs <outdir> <username> <password> [locale]
import { chromium } from "playwright";
import fs from "node:fs";

const [outDir, username, password, locale = "vi"] = process.argv.slice(2);
const BASE = process.env.GUIDE_BASE || "https://maildev.example.com";
fs.mkdirSync(outDir, { recursive: true });

const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1, locale: locale === "vi" ? "vi-VN" : locale === "ja" ? "ja-JP" : "en-US" });
const page = await ctx.newPage();
await page.addInitScript((loc) => {
  try { localStorage.setItem("maildev.locale", loc); localStorage.setItem("maildev.theme", "light"); } catch {}
}, locale);

// Highlight helper: draws a red outline + number badge around elements matching selectors.
async function highlight(selectors) {
  await page.evaluate((sels) => {
    document.querySelectorAll(".__hl").forEach((n) => n.remove());
    sels.forEach((sel, i) => {
      const el = document.querySelector(sel);
      if (!el) return;
      const r = el.getBoundingClientRect();
      const box = document.createElement("div");
      box.className = "__hl";
      Object.assign(box.style, { position: "fixed", left: r.left - 4 + "px", top: r.top - 4 + "px", width: r.width + 8 + "px", height: r.height + 8 + "px", border: "3px solid #e11d48", borderRadius: "8px", boxShadow: "0 0 0 3px rgba(225,29,72,.25)", zIndex: 99999, pointerEvents: "none" });
      const badge = document.createElement("div");
      badge.className = "__hl";
      badge.textContent = String(i + 1);
      Object.assign(badge.style, { position: "fixed", left: r.left - 14 + "px", top: r.top - 14 + "px", width: "24px", height: "24px", borderRadius: "12px", background: "#e11d48", color: "#fff", font: "bold 13px sans-serif", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 100000, pointerEvents: "none" });
      document.body.append(box, badge);
    });
  }, selectors);
}
async function shot(name, selectors = [], opts = {}) {
  await page.waitForTimeout(600);
  if (selectors.length) await highlight(selectors);
  await page.screenshot({ path: `${outDir}/${name}.png`, fullPage: !!opts.fullPage });
  await page.evaluate(() => document.querySelectorAll(".__hl").forEach((n) => n.remove()));
  console.log("shot", name);
}

// 1. login
await page.goto(BASE + "/login");
await page.waitForSelector("input[autocomplete=username]");
await shot("01-login", ["form.panel", "a[href='/guide/']", ".seg", "button[aria-label='Toggle theme']"]);
await page.fill("input[autocomplete=username]", username);
await page.fill("input[autocomplete=current-password]", password);
await page.click("form button[type=submit], form .btn-primary");
await page.waitForURL((u) => !u.pathname.startsWith("/login"), { timeout: 20000 });

// 2. home
await page.goto(BASE + "/");
await page.waitForSelector(".pcard");
await shot("02-home", [".pcard", "section .grid", "header nav", "header .seg"]);
await page.hover(".pcard");
await shot("03-home-hover", [".pcard-active", "section .grid a:nth-child(1)", "section .grid a:nth-child(2)"]);
await page.click(".pcard button[title]"); // first move button maybe; harmless

// 3. project pages (demo)
await page.goto(BASE + "/projects/demo");
await page.waitForSelector(".tab");
await shot("04-project-hub", ["div.flex.items-center.gap-1.border-b", ".grid a:nth-child(1)", ".grid a:nth-child(2)"]);
await page.goto(BASE + "/projects/demo/dev");
await page.waitForSelector(".acard");
await shot("05-dev-credentials", [".grid.md\\:grid-cols-3", "button.btn-danger", ".panel.grid nav", ".panel.grid .seg"]);
await page.click(".panel.grid nav button:nth-child(2)");
await shot("06-dev-outlook", [".panel.grid .code"]);
await page.click(".panel.grid nav button:nth-child(4)");
await page.click(".panel.grid .seg button:nth-child(2)");
await shot("07-dev-sendgrid", [".panel.grid .seg", ".panel.grid .code"]);
await page.goto(BASE + "/projects/demo/inbox");
await page.waitForSelector(".cmdbar");
await page.waitForTimeout(1500);
const firstMail = await page.$("div.group.cursor-pointer");
if (firstMail) await firstMail.click();
await page.waitForTimeout(1500);
await shot("08-inbox", [".cmdbar", "nav.hidden.w-48", "div.w-full.shrink-0.flex-col.border-r", "section.hidden.min-w-0"]);
const htmlBtn = await page.$("section .seg button:nth-child(2)");
if (htmlBtn) { await htmlBtn.click(); await shot("09-inbox-text", ["section .seg"]); }
await page.goto(BASE + "/projects/demo/relay");
await page.waitForSelector("h1");
await shot("10-relay-queue", ["h1", "select.input"]);
await page.goto(BASE + "/projects/demo/manage");
await page.waitForSelector(".tab");
await shot("11-manage-members", ["form.mb-4", "table.tbl"]);
await page.click("div.mb-4.flex.border-b .tab:nth-child(2)");
await shot("12-manage-settings", ["form.panel"]);
await page.click("div.mb-4.flex.border-b .tab:nth-child(3)");
await page.waitForTimeout(800);
await shot("13-manage-relay", [".panel:nth-of-type(1)", ".panel:nth-of-type(2)"]);
await page.click("div.mb-4.flex.border-b .tab:nth-child(4)");
await page.waitForTimeout(1200);
await shot("14-manage-audit", ["table.tbl"]);

// 4. admin
await page.goto(BASE + "/admin?tab=projects");
await page.waitForSelector("table.tbl");
await shot("15-admin-projects", [".seg", "table.tbl"]);
await page.goto(BASE + "/admin?tab=users");
await page.waitForSelector("table.tbl");
await shot("16-admin-users", ["input.input.w-64", ".seg", "button.btn-primary"]);
await page.goto(BASE + "/admin?tab=logs");
await page.waitForSelector("table.tbl");
await shot("17-admin-logs", ["select.input", "table.tbl"]);

// 5. dark theme sample
await page.evaluate(() => { localStorage.setItem("maildev.theme", "dark"); document.documentElement.dataset.theme = "dark"; });
await page.goto(BASE + "/projects/demo/inbox");
await page.waitForTimeout(1500);
await shot("18-inbox-dark");

await browser.close();
console.log("done");
