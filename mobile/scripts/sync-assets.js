#!/usr/bin/env node
/**
 * sync-assets.js
 * Synchronizes Scalper web frontend static assets into the Capacitor mobile web bundle.
 */

const fs = require('fs');
const path = require('path');

const ROOT_DIR = path.resolve(__dirname, '..');
const STATIC_SRC = path.resolve(ROOT_DIR, '..', 'internal', 'web', 'static');
const WWW_DIR = path.resolve(ROOT_DIR, 'www');
const WWW_STATIC_DIR = path.resolve(WWW_DIR, 'static');
const ANDROID_ASSETS_DIR = path.resolve(ROOT_DIR, 'android', 'app', 'src', 'main', 'assets', 'public');
const IOS_ASSETS_DIR = path.resolve(ROOT_DIR, 'ios', 'App', 'App', 'public');

function ensureDir(dir) {
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(dir, { recursive: true });
  }
}

function copyFile(src, dst) {
  fs.copyFileSync(src, dst);
}

function copyDirClean(src, dst) {
  ensureDir(dst);
  const entries = fs.readdirSync(src, { withFileTypes: true });
  for (const entry of entries) {
    if (entry.isDirectory()) {
      continue;
    }
    // Skip brotli pre-compressed files (.br) for local mobile webview bundle
    if (entry.name.endsWith('.br')) {
      continue;
    }
    const srcPath = path.join(src, entry.name);
    const dstPath = path.join(dst, entry.name);
    copyFile(srcPath, dstPath);
  }
}

console.log('[sync-assets] Syncing Scalper frontend assets for Capacitor mobile...');

if (!fs.existsSync(STATIC_SRC)) {
  console.error(`[sync-assets] Error: Source static directory does not exist: ${STATIC_SRC}`);
  process.exit(1);
}

ensureDir(WWW_DIR);
ensureDir(WWW_STATIC_DIR);

// Copy static assets into www/ and www/static/
copyDirClean(STATIC_SRC, WWW_STATIC_DIR);
copyDirClean(STATIC_SRC, WWW_DIR);

// Copy to Android and iOS asset folders if they exist
if (fs.existsSync(path.dirname(ANDROID_ASSETS_DIR))) {
  ensureDir(ANDROID_ASSETS_DIR);
  copyDirClean(STATIC_SRC, ANDROID_ASSETS_DIR);
  ensureDir(path.join(ANDROID_ASSETS_DIR, 'static'));
  copyDirClean(STATIC_SRC, path.join(ANDROID_ASSETS_DIR, 'static'));
  if (fs.existsSync(path.join(WWW_DIR, 'index.html'))) {
    copyFile(path.join(WWW_DIR, 'index.html'), path.join(ANDROID_ASSETS_DIR, 'index.html'));
  }
}

if (fs.existsSync(path.dirname(IOS_ASSETS_DIR))) {
  ensureDir(IOS_ASSETS_DIR);
  copyDirClean(STATIC_SRC, IOS_ASSETS_DIR);
  ensureDir(path.join(IOS_ASSETS_DIR, 'static'));
  copyDirClean(STATIC_SRC, path.join(IOS_ASSETS_DIR, 'static'));
  if (fs.existsSync(path.join(WWW_DIR, 'index.html'))) {
    copyFile(path.join(WWW_DIR, 'index.html'), path.join(IOS_ASSETS_DIR, 'index.html'));
  }
}

console.log('[sync-assets] Successfully synced web assets into www/, android, and ios bundles.');
