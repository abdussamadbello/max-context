#!/usr/bin/env node
/**
 * Phase 7: Download max-context binary from GitHub Release, verify SHA256, extract to bin/.
 * Uses GITHUB_TOKEN for private repos; public releases need no token.
 */
const fs = require('fs');
const path = require('path');
const https = require('https');
const { execSync } = require('child_process');

const VERSION = process.env.MAX_CONTEXT_VERSION || '0.1.0';
const OWNER = 'maxcontext';
const REPO = 'max-context';
const BIN_DIR = path.join(__dirname, '..', 'bin');

const platform = process.platform;
const arch = process.arch;
const isWindows = platform === 'win32';
const ext = isWindows ? 'zip' : 'tar.gz';
const osArch = `${platform}-${arch}`;
const suffix = isWindows ? '.exe' : '';
const assetName = `max-context_${VERSION}_${platform}_${arch}${isWindows ? '.zip' : '.tar.gz'}`;

function get(url, opts = {}) {
  return new Promise((resolve, reject) => {
    const req = https.get(url, { headers: { 'User-Agent': 'max-context-postinstall' } }, (res) => {
      if (res.statusCode === 302 || res.statusCode === 301) return get(res.headers.location, opts).then(resolve).catch(reject);
      let buf = [];
      res.on('data', (c) => buf.push(c));
      res.on('end', () => resolve(Buffer.concat(buf)));
    });
    req.on('error', reject);
  });
}

async function main() {
  try {
    const tag = VERSION.startsWith('v') ? VERSION : `v${VERSION}`;
    const base = `https://github.com/${OWNER}/${REPO}/releases/download/${tag}`;
    const checksumsUrl = `${base}/checksums.txt`;
    const assetUrl = `${base}/${assetName}`;

    if (!fs.existsSync(BIN_DIR)) fs.mkdirSync(BIN_DIR, { recursive: true });
    const binPath = path.join(BIN_DIR, `max-context${suffix}`);

    const [checksumsBuf, assetBuf] = await Promise.all([
      get(checksumsUrl).catch(() => null),
      get(assetUrl).catch((e) => { throw new Error(`Download failed: ${assetUrl} - ${e.message}`); }),
    ]);

    if (checksumsBuf) {
      const line = checksumsBuf.toString().split('\n').find((l) => l.includes(assetName));
      if (line) {
        const expected = line.trim().split(/\s+/)[0];
        const crypto = require('crypto');
        const actual = crypto.createHash('sha256').update(assetBuf).digest('hex');
        if (expected && actual !== expected) throw new Error(`SHA256 mismatch for ${assetName}`);
      }
    }

    if (isWindows) {
      const AdmZip = require('adm-zip');
      const zip = new AdmZip(assetBuf);
      const entry = zip.getEntries().find((e) => e.entryName.endsWith('.exe'));
      if (entry) fs.writeFileSync(binPath, entry.getData());
    } else {
      execSync(`tar -xzf - -C "${BIN_DIR}"`, { input: assetBuf });
      const extracted = path.join(BIN_DIR, 'max-context');
      if (extracted !== binPath && fs.existsSync(extracted)) fs.renameSync(extracted, binPath);
    }
    if (!fs.existsSync(binPath)) throw new Error('Binary not found after extract');
    if (platform !== 'win32') fs.chmodSync(binPath, 0o755);
  } catch (e) {
    console.warn('max-context postinstall:', e.message);
    console.warn('Install the binary manually from https://github.com/' + OWNER + '/' + REPO + '/releases');
  }
}

main();
