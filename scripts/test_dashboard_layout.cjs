#!/usr/bin/env node
'use strict';

const { spawn } = require('node:child_process');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const net = require('node:net');

const root = path.resolve(__dirname, '..');
const outputDir = process.argv[2] || path.join(os.tmpdir(), 'family-hq-layout');
const chromiumPath = process.env.CHROMIUM || '/usr/bin/chromium';
const prefix = process.env.DASHBOARD_LAYOUT_PREFIX || 'after';
const captureOnly = process.env.DASHBOARD_LAYOUT_CAPTURE_ONLY === '1';
const kioskLayout = process.env.DASHBOARD_LAYOUT_KIOSK === '1';

function delay(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

async function freePort() {
  const server = net.createServer();
  await new Promise((resolve, reject) => server.listen(0, '127.0.0.1', resolve).once('error', reject));
  const { port } = server.address();
  await new Promise(resolve => server.close(resolve));
  return port;
}

async function waitFor(check, description) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    try {
      const result = await check();
      if (result) return result;
    } catch {}
    await delay(250);
  }
  throw new Error(`Timed out waiting for ${description}`);
}

async function main() {
  await fs.mkdir(outputDir, { recursive: true });
  const externalURL = process.env.DASHBOARD_LAYOUT_URL;
  const appPort = externalURL ? null : await freePort();
  const debugPort = await freePort();
  const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'family-hq-layout-chromium-'));
  const taskFile = path.join(profile, 'tasks.json');
  const app = externalURL ? null : spawn('go', ['run', '.'], {
    cwd: root,
    stdio: 'ignore',
    env: {
      ...process.env,
      FAMILY_DASHBOARD_DEV_MODE: '1',
      FAMILY_AUTH_MODE: 'off',
      LISTEN_ADDR: `127.0.0.1:${appPort}`,
      TASKS_FILE: taskFile,
    },
  });
  let browser;
  let ws;
  try {
    const url = externalURL || `http://127.0.0.1:${appPort}/dashboard`;
    await waitFor(async () => (await fetch(url)).ok, 'the local dashboard');
    browser = spawn(chromiumPath, [
      '--headless=new', '--no-sandbox', '--disable-gpu', '--no-first-run',
      `--remote-debugging-port=${debugPort}`, `--user-data-dir=${path.join(profile, 'browser')}`, url,
    ], { stdio: 'ignore' });
    const page = await waitFor(async () => {
      const targets = await (await fetch(`http://127.0.0.1:${debugPort}/json/list`)).json();
      return targets.find(target => target.type === 'page');
    }, 'Chromium DevTools');
    ws = new WebSocket(page.webSocketDebuggerUrl);
    await new Promise((resolve, reject) => {
      ws.addEventListener('open', resolve, { once: true });
      ws.addEventListener('error', reject, { once: true });
    });

    let sequence = 0;
    const pending = new Map();
    ws.addEventListener('message', event => {
      const message = JSON.parse(event.data);
      if (message.id && pending.has(message.id)) {
        pending.get(message.id)(message);
        pending.delete(message.id);
      }
    });
    function cdp(method, params = {}) {
      return new Promise((resolve, reject) => {
        const id = ++sequence;
        pending.set(id, message => message.error ? reject(new Error(message.error.message)) : resolve(message));
        ws.send(JSON.stringify({ id, method, params }));
      });
    }
    async function evaluate(expression) {
      const result = await cdp('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
      if (result.result.exceptionDetails) throw new Error(result.result.exceptionDetails.text);
      return result.result.result.value;
    }

    await cdp('Page.enable');
    await cdp('Runtime.enable');
    await waitFor(() => evaluate("document.querySelector('.calendar') && document.querySelector('.feature')"), 'dashboard panels');
    await waitFor(() => evaluate("document.readyState === 'complete'"), 'dashboard scripts');
    await delay(500);
    if (kioskLayout) await evaluate("document.body.dataset.kiosk = 'true'");
    await evaluate(`(() => {
      const calendar = document.querySelector('.calendar');
      const routine = document.createElement('section');
      routine.className = 'school-routine';
      routine.setAttribute('aria-label', 'School routine fixture');
      routine.innerHTML = '<div class="school-routine-head"><strong>School morning</strong><span>Leave on time</span></div><div class="school-wear school-wear-shared">Warm layers · rain jacket · bring a coat for the colder afternoon</div><div class="school-card-grid"><article class="school-card"><div class="school-card-name">Ayla</div><div class="school-timer">8m</div><div class="school-caption">leave by · 8:23 AM</div><div class="school-late">Late in <strong>28m</strong></div><div class="school-pickup-alert" role="alert">Rain pickup · leave in <strong>38m</strong></div></article><article class="school-card"><div class="school-card-name">Wyatt</div><div class="school-timer">12m</div><div class="school-caption">leave by · 8:27 AM</div><div class="school-late">Late in <strong>25m</strong></div></article></div>';
      calendar.querySelector('.week-strip').after(routine);

      const feature = document.querySelector('.feature');
      const review = document.createElement('section');
      review.className = 'travel-review has-pending essentials-travel';
      review.id = 'layout-travel-fixture';
      review.innerHTML = '<div class="travel-review-head"><div><p class="eyebrow">TRAVEL TO DECIDE</p><h3>Upcoming events need a choice</h3></div><span class="travel-review-count">1</span></div><p class="travel-review-intro">Choose the travel time each way. Series decisions apply to the series.</p><div class="travel-review-list"><article class="travel-review-item"><div class="travel-review-event"><strong>Saturday activity with an intentionally long family title</strong><span>Nov 14 · 10:00 AM</span></div><div class="travel-choice-grid"><button class="travel-choice no-travel">No travel</button><button class="travel-choice">10 min</button><button class="travel-choice">15 min</button><button class="travel-choice">20 min</button><button class="travel-choice">30 min</button><button class="travel-choice">Custom</button></div></article></div>';
      feature.querySelector('.feature-header').after(review);
      return true;
    })()`);

    async function capture(name, width, height) {
      await cdp('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: false });
      await delay(200);
      const metrics = await evaluate(`(() => {
        const box = selector => { const element = document.querySelector(selector); if (!element) return null; const rect = element.getBoundingClientRect(), style=getComputedStyle(element); return {x:rect.x,y:rect.y,width:rect.width,height:rect.height,clientHeight:element.clientHeight,scrollHeight:element.scrollHeight,overflowY:style.overflowY,minHeight:style.minHeight,gridRows:style.gridTemplateRows,gridColumn:style.gridColumn,padding:style.padding,borderTopWidth:style.borderTopWidth,borderRightWidth:style.borderRightWidth,borderBottomWidth:style.borderBottomWidth}; };
        return {viewport:{width:innerWidth,height:innerHeight},document:{width:document.documentElement.scrollWidth,height:document.documentElement.scrollHeight},shell:box('.shell'),header:box('.header'),banner:box('.celebration-banner'),dashboard:box('.dashboard'),calendar:box('.calendar'),routine:box('.school-routine'),ayla:box('.school-card'),feature:box('.feature'),travel:box('#layout-travel-fixture'),buttons:[...document.querySelectorAll('#layout-travel-fixture .travel-choice')].map(button=>{const rect=button.getBoundingClientRect();return {text:button.textContent.trim(),x:rect.x,y:rect.y,width:rect.width,height:rect.height}})};
      })()`);
      const screenshot = await cdp('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
      await fs.writeFile(path.join(outputDir, `${name}.png`), Buffer.from(screenshot.result.data, 'base64'));
      return metrics;
    }

    const short = await capture(`${prefix}-1146x531`, 1146, 531);
    const dense = await capture(`${prefix}-1063x465`, 1063, 465);
    if (!captureOnly) {
      if (dense.document.height > 465) throw new Error(`1063×465 layout scrolls the page: ${JSON.stringify(dense.document)}`);
      if (dense.header.height > 60 || dense.banner.height > 24) throw new Error(`Holiday title is not in the compact header row: ${JSON.stringify({header:dense.header,banner:dense.banner})}`);
      if (dense.buttons.length !== 6 || dense.buttons.some(button => button.width < 70 || button.height < 36 || button.y < dense.travel.y || button.y + button.height > dense.travel.y + dense.travel.height || button.y + button.height > 465)) {
        throw new Error(`Travel choices are clipped at 1063×465: ${JSON.stringify(dense.buttons)}`);
      }
      if (dense.travel.borderTopWidth !== '0px' || dense.travel.borderRightWidth !== '0px' || dense.travel.borderBottomWidth !== '0px') throw new Error(`Travel queue has a redundant inset frame: ${JSON.stringify(dense.travel)}`);
      const denseSchool = await evaluate("['.school-timer','.school-late','.school-pickup-alert'].map(selector=>{const e=document.querySelector(selector),r=e?.getBoundingClientRect();return {selector,visible:!!e&&!!e.getClientRects().length,top:r?.top,bottom:r?.bottom}})");
      if (denseSchool.some(item => !item.visible || item.top < 0 || item.bottom > 465)) throw new Error(`School details are clipped at 1063×465: ${JSON.stringify(denseSchool)}`);
      await cdp('Emulation.setDeviceMetricsOverride', { width: 1146, height: 531, deviceScaleFactor: 1, mobile: false });
      if (short.document.height > 531) throw new Error(`Short layout scrolls the page: ${JSON.stringify(short.document)}`);
      if (short.banner.height > 60) throw new Error(`Holiday header was not compacted: ${short.banner.height}px`);
      if (!short.ayla || short.ayla.height < 76) throw new Error(`Ayla school details are too compressed: ${JSON.stringify(short.ayla)}`);
      const buttons = short.buttons;
      if (buttons.length !== 6 || buttons.some(button => button.width < 70 || button.height < 36 || button.y < short.travel.y || button.y + button.height > short.travel.y + short.travel.height || button.y + button.height > 531)) {
        throw new Error(`Travel choices are clipped or too small: ${JSON.stringify(buttons)}`);
      }
      const criticalSchoolDetails = await evaluate("['.school-timer','.school-late','.school-pickup-alert'].map(selector=>{const e=document.querySelector(selector),r=e?.getBoundingClientRect();return {selector,visible:!!e&&!!e.getClientRects().length,top:r?.top,bottom:r?.bottom}})");
      if (criticalSchoolDetails.some(item => !item.visible || item.top < 0 || item.bottom > 531)) throw new Error(`Critical school details are outside the visible viewport: ${JSON.stringify(criticalSchoolDetails)}`);
      const schoolOverlap = await evaluate(`(() => [...document.querySelectorAll('.school-card')].some(card => {
        const items = [...card.children].filter(item => item.getClientRects().length).map(item => item.getBoundingClientRect());
        return items.some((a,index) => items.slice(index+1).some(b => a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top));
      }))()`);
      if (schoolOverlap) throw new Error('School card text blocks overlap at 1146×531');
      const scrollModel = await evaluate("({calendar:getComputedStyle(document.querySelector('.calendar')).overflowY,feature:getComputedStyle(document.querySelector('.feature')).overflowY,review:getComputedStyle(document.querySelector('#layout-travel-fixture')).overflowY,list:getComputedStyle(document.querySelector('#layout-travel-fixture .travel-review-list')).overflowY})");
      if (scrollModel.calendar !== 'auto' || scrollModel.feature !== 'auto' || scrollModel.review !== 'visible' || scrollModel.list !== 'visible') {
        throw new Error(`Unexpected nested scroll containers: ${JSON.stringify(scrollModel)}`);
      }
      if (kioskLayout) await evaluate("document.body.dataset.kiosk = 'false'");
      await cdp('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
      await evaluate("document.querySelector('#tab-tasks').click()");
      const tasksState = await evaluate("({selected:document.querySelector('[role=tab][aria-selected=true]')?.id, tasksHidden:document.querySelector('#pane-tasks').hidden})");
      if (tasksState.selected !== 'tab-tasks' || tasksState.tasksHidden) throw new Error(`Task tab control did not remain operable: ${JSON.stringify(tasksState)}`);
      await evaluate("document.querySelector('#feature-next').click()");
      const nextState = await evaluate("({selected:document.querySelector('[role=tab][aria-selected=true]')?.id, camerasHidden:document.querySelector('#pane-cameras').hidden})");
      if (nextState.selected !== 'tab-cameras' || nextState.camerasHidden) throw new Error(`Feature navigation did not remain operable: ${JSON.stringify(nextState)}`);
      await evaluate("document.querySelector('#tab-weather').click()");
      if (kioskLayout) await evaluate("document.body.dataset.kiosk = 'true'");
    }
    const tv = await capture(`${prefix}-1920x1080`, 1920, 1080);
    const desktop = await capture(`${prefix}-1280x720`, 1280, 720);
    await cdp('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
    const touch = await capture(`${prefix}-1280x800-touch`, 1280, 800);
    await cdp('Emulation.setTouchEmulationEnabled', { enabled: false });
    if (!captureOnly) {
      if (tv.document.height > 1080) throw new Error(`TV layout scrolls the page: ${JSON.stringify(tv.document)}`);
      if (desktop.document.height > 720) throw new Error(`Desktop layout scrolls the page: ${JSON.stringify(desktop.document)}`);
      if (touch.document.height > 800) throw new Error(`Touch layout scrolls the page: ${JSON.stringify(touch.document)}`);
      console.log(`Layout assertions passed. Screenshots: ${outputDir}`);
      console.log(JSON.stringify({ dense: { header:dense.header, banner:dense.banner, school:dense.ayla, feature:dense.feature, travel:dense.travel }, short: { header: short.header, banner: short.banner, school: short.ayla, feature: short.feature, travel: short.travel }, tv: { dashboard: tv.dashboard } }, null, 2));
    } else {
      console.log(`Fixture screenshots captured. Screenshots: ${outputDir}`);
    }
  } finally {
    if (ws) ws.close();
    if (browser) browser.kill('SIGTERM');
    if (app) app.kill('SIGTERM');
    await delay(300);
    await fs.rm(profile, { recursive: true, force: true });
  }
}

main().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
