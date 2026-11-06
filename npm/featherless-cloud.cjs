#!/usr/bin/env node
// Runs the featherless-cloud binary from the platform package npm installed
// alongside this one (@featherlessai/gpu-cloud-cli-<os>-<arch>).
'use strict';

const { spawnSync } = require('node:child_process');
const path = require('node:path');

const ARCHES = { arm64: 'arm64', x64: 'x64' };
const PLATFORMS = { darwin: 'darwin', linux: 'linux', win32: 'win32' };

function binaryPath() {
  const platform = PLATFORMS[process.platform];
  const arch = ARCHES[process.arch];

  if (!platform || !arch) {
    throw new Error(`featherless-cloud does not support ${process.platform}-${process.arch}.`);
  }

  const name = `@featherlessai/gpu-cloud-cli-${platform}-${arch}`;
  let manifest;

  try {
    manifest = require.resolve(`${name}/package.json`);
  } catch {
    throw new Error(`${name} is missing. Reinstall with: npm install --global @featherlessai/gpu-cloud-cli`);
  }

  const executable = platform === 'win32' ? 'featherless-cloud.exe' : 'featherless-cloud';
  return path.join(path.dirname(manifest), 'bin', executable);
}

let binary;

try {
  binary = binaryPath();
} catch (error) {
  console.error(`Error: ${error.message}`);
  process.exit(1);
}

const result = spawnSync(binary, process.argv.slice(2), { stdio: 'inherit' });

if (result.error) {
  console.error(`Error: Could not run ${binary}: ${result.error.message}`);
  process.exit(1);
}

if (result.signal) {
  process.kill(process.pid, result.signal);
}

process.exit(result.status ?? 1);
