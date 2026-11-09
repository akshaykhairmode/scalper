#!/usr/bin/env node
/**
 * sync-assets.js
 * Synchronizes the Capacitor mobile pairing shell into the native web bundles.
 *
 * The mobile app is a pairing screen that navigates to the user's Scalper
 * server; it does not run the terminal UI locally. Only the shell, jsQR and
 * icons are needed, which keeps the native bundles small.
 */

const fs = require('fs');
const path = require('path');

const ROOT_DIR = path.resolve(__dirname, '..');
const STATIC_SRC = path.resolve(ROOT_DIR, '..', 'internal', 'web', 'static');
const WWW_DIR = path.resolve(ROOT_DIR, 'www');
const ANDROID_ASSETS_DIR = path.resolve(ROOT_DIR, 'android', 'app', 'src', 'main', 'assets', 'public');
const IOS_ASSETS_DIR = path.resolve(ROOT_DIR, 'ios', 'App', 'App', 'public');

const ROOT_FILES = [
  'index.html',
  'jsqr.js',
  'manifest.json',
  'manifest.webmanifest',
  'sw.js',
];
const STATIC_FILES = [
  'icon.svg',
  'icon-192.png',
  'icon-512.png',
  'icon-maskable-192.png',
  'icon-maskable-512.png',
  'apple-touch-icon.png',
];

function ensureDir(dir) {
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(dir, { recursive: true });
  }
}

function cleanAndCopy(src, dst, allowed) {
  ensureDir(dst);
  for (const entry of fs.readdirSync(dst, { withFileTypes: true })) {
    if (entry.isDirectory()) continue;
    if (!allowed.has(entry.name)) {
      fs.rmSync(path.join(dst, entry.name), { force: true });
    }
  }
  for (const name of allowed) {
    const srcPath = path.join(src, name);
    if (fs.existsSync(srcPath)) {
      fs.copyFileSync(srcPath, path.join(dst, name));
    }
  }
}

function syncTarget(dstRoot) {
  cleanAndCopy(STATIC_SRC, dstRoot, new Set(ROOT_FILES));
  cleanAndCopy(STATIC_SRC, path.join(dstRoot, 'static'), new Set(STATIC_FILES));
}

console.log('[sync-assets] Syncing Scalper mobile pairing shell...');

if (!fs.existsSync(STATIC_SRC)) {
  console.error(`[sync-assets] Error: Source static directory does not exist: ${STATIC_SRC}`);
  process.exit(1);
}

syncTarget(WWW_DIR);

if (fs.existsSync(path.dirname(ANDROID_ASSETS_DIR))) {
  syncTarget(ANDROID_ASSETS_DIR);
}

if (fs.existsSync(path.dirname(IOS_ASSETS_DIR))) {
  syncTarget(IOS_ASSETS_DIR);
}

console.log('[sync-assets] Successfully synced the mobile shell into www/, android, and ios bundles.');
