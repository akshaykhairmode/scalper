import { test } from "node:test";
import assert from "node:assert/strict";
import {
  replaceYamlBlock,
  setYamlList,
  setYamlScalar,
  yamlQuote,
  yamlScalarValue,
  yamlTrailingComment,
} from "../src/settings/yaml.ts";

test("yamlQuote produces a YAML/JSON-safe double-quoted scalar", () => {
  const value = 'C:\\temp\\file "quoted"\nnext';
  const quoted = yamlQuote(value);
  assert.equal(JSON.parse(quoted), value);
  assert.ok(quoted.startsWith('"') && quoted.endsWith('"'));
  assert.ok(quoted.includes("\\\\"));
  assert.ok(quoted.includes('\\"'));
  assert.ok(quoted.includes("\\n"));
});

test("yaml scalar helpers split values from trailing comments", () => {
  assert.equal(yamlScalarValue('"NIFTY"   # key comment'), "NIFTY");
  assert.equal(yamlTrailingComment('"NIFTY"   # key comment'), "# key comment");
  assert.equal(yamlScalarValue("'A''B'"), "A'B");
  assert.equal(yamlTrailingComment('"a # b"'), "");
});

test("setYamlScalar preserves a trailing comment and escapes the value", () => {
  const yaml = 'app:\n  db_path: "./old" # keep me\n';
  const out = setYamlScalar(yaml, ["app", "db_path"], 'C:\\data\\x "y"', "string");
  assert.equal(out, `app:\n  db_path: ${yamlQuote('C:\\data\\x "y"')} # keep me\n`);
});

test("setYamlScalar inserts a missing key under the existing section", () => {
  const yaml = 'market:\n  data_mode: "websocket"\n';
  const out = setYamlScalar(yaml, ["market", "hsm_url"], "wss://example.test/ws", "string");
  assert.equal(out, 'market:\n  data_mode: "websocket"\n  hsm_url: "wss://example.test/ws"\n');
});

test("setYamlScalar respects a 4-space indentation style", () => {
  const yaml = 'market:\n    data_mode: "websocket"\n    chain:\n        batch_size: 40\n';
  const out = setYamlScalar(yaml, ["market", "hsm_url"], "wss://example.test/ws", "string");
  const line = out.split("\n").find((entry) => entry.includes("hsm_url:"));
  assert.equal(line, '    hsm_url: "wss://example.test/ws"');
});

test("setYamlList replaces an inline list and keeps its comment", () => {
  const yaml = 'market:\n  holidays: [] # e.g. ["2026-10-02"]\n';
  const out = setYamlList(yaml, ["market", "holidays"], ["2026-01-01", "2026-01-02"]);
  assert.equal(out, 'market:\n  holidays: ["2026-01-01", "2026-01-02"] # e.g. ["2026-10-02"]\n');
});

test("setYamlList keeps block comments without reconnecting old items", () => {
  const yaml = [
    "market:",
    "  holidays:",
    "    # exchange holidays",
    '    - "2026-01-01"',
    "    # keep this note",
    '    - "2026-01-02" # fixed',
    "  chain:",
    "    batch_size: 40",
    "",
  ].join("\n");
  const out = setYamlList(yaml, ["market", "holidays"], ["2026-12-25"]);
  assert.ok(out.includes("    # exchange holidays"));
  assert.ok(out.includes("    # keep this note"));
  assert.ok(!out.includes("2026-01-01"));
  assert.ok(!out.includes("2026-01-02"));
  assert.ok(out.includes('    - "2026-12-25"'));
  assert.ok(out.includes("  chain:"));
});

test("setYamlList honours a 4-space block list", () => {
  const yaml = 'market:\n    holidays:\n        - "2026-01-01"\n    chain:\n        batch_size: 40\n';
  const out = setYamlList(yaml, ["market", "holidays"], ["2026-12-25"]);
  assert.ok(out.includes('        - "2026-12-25"'));
  assert.ok(out.includes("    chain:"));
});

test("setYamlList turns an emptied block list into [] while keeping its comment", () => {
  const yaml = 'market:\n  holidays:\n    # holiday note\n    - "2026-01-01"\n  chain:\n    batch_size: 40\n';
  const out = setYamlList(yaml, ["market", "holidays"], []);
  assert.ok(out.includes("  # holiday note"));
  assert.ok(out.includes("  holidays: []"));
  assert.ok(!out.includes("2026-01-01"));
  assert.ok(out.includes("  chain:"));
});

test("replaceYamlBlock keeps the key comment, standalone comments and siblings", () => {
  const yaml = 'market:\n  indices: # configured indices\n    # note\n    - key: "NIFTY"\n  chain:\n    batch_size: 40\n';
  const block = ['    - key: "BANKNIFTY"', '      display_name: "BANKNIFTY"'];
  const out = replaceYamlBlock(yaml, ["market", "indices"], block);
  assert.ok(out.includes("  indices: # configured indices"));
  assert.ok(out.includes("    # note"));
  assert.ok(!out.includes('- key: "NIFTY"'));
  assert.ok(out.includes("  chain:"));
});

test("replaceYamlBlock writes an empty list as []", () => {
  const yaml = 'market:\n  commodities:\n    - key: "GOLD"\n  chain: {}\n';
  const out = replaceYamlBlock(yaml, ["market", "commodities"], []);
  assert.ok(out.includes("  commodities: []"));
  assert.ok(!out.includes("GOLD"));
});
