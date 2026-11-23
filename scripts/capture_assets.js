const puppeteer = require('puppeteer-core');
const fs = require('fs');
const path = require('path');
const { spawn, execSync } = require('child_process');

const EDGE_PATH = 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe';
const BIN_PATH = path.join(__dirname, '..', 'bin', 'shadowtap.exe');
const IMAGES_DIR = path.join(__dirname, '..', 'docs', 'images');
const FRAMES_DIR = path.join(__dirname, '..', 'docs', 'frames');

async function waitForServer(url, timeoutMs = 15000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    try {
      const res = await fetch(url);
      if (res.ok) return true;
    } catch (e) {
      // Wait and retry
    }
    await new Promise(r => setTimeout(r, 400));
  }
  throw new Error(`Server at ${url} did not become ready within ${timeoutMs}ms`);
}

async function run() {
  if (!fs.existsSync(IMAGES_DIR)) {
    fs.mkdirSync(IMAGES_DIR, { recursive: true });
  }
  if (!fs.existsSync(FRAMES_DIR)) {
    fs.mkdirSync(FRAMES_DIR, { recursive: true });
  }

  console.log('Spawning shadowtap demo server...');
  const demoProc = spawn(BIN_PATH, ['demo'], {
    stdio: 'inherit'
  });

  try {
    console.log('Waiting for Cockpit server on http://localhost:8989...');
    await waitForServer('http://localhost:8989');
    console.log('Cockpit server is live and ready!');

    const browser = await puppeteer.launch({
      executablePath: EDGE_PATH,
      headless: 'new',
      args: ['--no-sandbox', '--disable-setuid-sandbox', '--disable-gpu']
    });

    const page = await browser.newPage();
    await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 2 });

    console.log('Navigating to Cockpit...');
    await page.goto('http://localhost:8989', { waitUntil: 'networkidle2' });

    // Wait 5 seconds for live traffic events to stream in via SSE
    console.log('Waiting for live traffic events...');
    await new Promise(r => setTimeout(r, 5000));

    // Select a regressed transaction card to display dual diff
    await page.evaluate(() => {
      const cards = Array.from(document.querySelectorAll('.tx-card'));
      const regressed = cards.find(c => c.querySelector('.tag-regress'));
      if (regressed) {
        regressed.click();
      } else if (cards.length > 0) {
        cards[0].click();
      }
    });

    await new Promise(r => setTimeout(r, 1200));

    // Capture Desktop Screenshot
    const desktopPath = path.join(IMAGES_DIR, 'cockpit_desktop.png');
    await page.screenshot({ path: desktopPath, fullPage: false });
    console.log('✓ Desktop screenshot captured at:', desktopPath);

    // Capture Mobile Screenshot
    await page.setViewport({ width: 390, height: 844, deviceScaleFactor: 2, isMobile: true });
    await new Promise(r => setTimeout(r, 1200));
    const mobilePath = path.join(IMAGES_DIR, 'cockpit_mobile.png');
    await page.screenshot({ path: mobilePath, fullPage: false });
    console.log('✓ Mobile screenshot captured at:', mobilePath);

    // Switch back to desktop for GIF frame recording
    await page.setViewport({ width: 1360, height: 840, deviceScaleFactor: 1 });
    await new Promise(r => setTimeout(r, 1000));

    console.log('Recording 20 frames for animated demo GIF...');
    for (let i = 0; i < 20; i++) {
      const framePath = path.join(FRAMES_DIR, `frame_${String(i).padStart(3, '0')}.png`);
      await page.screenshot({ path: framePath });
      // Click alternating cards every 5 frames to show interactive diff updating
      if (i % 5 === 0) {
        await page.evaluate((idx) => {
          const cards = Array.from(document.querySelectorAll('.tx-card'));
          const target = cards[idx % cards.length];
          if (target) target.click();
        }, i / 5);
      }
      await new Promise(r => setTimeout(r, 200));
    }

    await browser.close();

    // Compile frames to GIF with ffmpeg
    const gifPath = path.join(IMAGES_DIR, 'shadowtap_demo.gif');
    console.log('Compiling frames to animated GIF via ffmpeg...');
    const ffmpegCmd = `ffmpeg -y -framerate 5 -i "${path.join(FRAMES_DIR, 'frame_%03d.png')}" -vf "fps=8,scale=1080:-1:flags=lanczos,split[s0][s1];[s0]palettegen=max_colors=128[p];[s1][p]paletteuse=dither=bayer" "${gifPath}"`;
    
    execSync(ffmpegCmd);
    console.log('✓ Animated demo GIF created at:', gifPath);

    // Clean up frames folder
    fs.rmSync(FRAMES_DIR, { recursive: true, force: true });
    console.log('✓ Cleaned up temporary frames.');
  } finally {
    console.log('Stopping shadowtap demo process...');
    demoProc.kill();
  }
}

run().catch(err => {
  console.error('Error during asset capture:', err);
  process.exit(1);
});
