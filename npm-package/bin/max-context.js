#!/usr/bin/env node
const { spawnSync } = require('child_process');
const path = require('path');
const bin = path.join(__dirname, '..', 'bin', process.platform === 'win32' ? 'max-context.exe' : 'max-context');
const { status } = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
process.exit(status !== null ? status : 1);
