/**
 * Line-preserving YAML editing helpers for the settings form.
 *
 * The settings page edits an existing YAML document instead of re-serializing
 * the whole config so comments, ordering and unrelated keys survive a save.
 * Only the edited value is rewritten.
 */

export type YamlScalarType = "string" | "int" | "float" | "bool";

/** A JSON string literal is also a valid YAML double-quoted scalar. */
export function yamlQuote(value: unknown): string {
  // JSON.stringify escapes backslashes, quotes, newlines and control chars.
  return JSON.stringify(String(value ?? ""));
}

function indentOf(line: string): number {
  let i = 0;
  while (i < line.length && line[i] === " ") i++;
  return i;
}

function isBlank(line: string): boolean {
  return line.trim() === "";
}

function isComment(line: string): boolean {
  return line.trim().startsWith("#");
}

/** Index of an unquoted, whitespace-prefixed YAML comment marker. */
function commentIndex(text: string): number {
  let quote = "";
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (quote) {
      if (quote === '"' && ch === "\\") {
        i++;
        continue;
      }
      if (ch === quote) quote = "";
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
      continue;
    }
    if (ch === "#" && (i === 0 || /\s/.test(text[i - 1]))) {
      return i;
    }
  }
  return -1;
}

function splitKeyLine(line: string) {
  const colon = line.indexOf(":");
  if (colon === -1) return null;
  const keyPart = line.slice(0, colon + 1);
  const after = line.slice(colon + 1);
  const hash = commentIndex(after);
  return {
    indent: indentOf(line),
    keyPart,
    comment: hash !== -1 ? " " + after.slice(hash).trim() : "",
    value: (hash !== -1 ? after.slice(0, hash) : after).trim(),
  };
}

function formatScalar(value: unknown, type: YamlScalarType): string {
  if (type === "bool") return value ? "true" : "false";
  if (type === "int" || type === "float") return String(value);
  return yamlQuote(value);
}

function detectIndentStep(lines: string[], start: number, end: number, baseIndent: number): number {
  let smallest = -1;
  for (let i = start; i < end; i++) {
    const line = lines[i];
    if (isBlank(line) || isComment(line)) continue;
    const indent = indentOf(line);
    if (indent > baseIndent && (smallest === -1 || indent < smallest)) {
      smallest = indent;
    }
  }
  return smallest === -1 ? 2 : smallest - baseIndent;
}

/** Insertion point after the last non-blank child of a block. */
function findBlockInsertLine(lines: string[], start: number, baseIndent: number): number {
  let lastChild = start;
  for (let i = start + 1; i < lines.length; i++) {
    const line = lines[i];
    if (isBlank(line)) continue;
    if (indentOf(line) <= baseIndent) break;
    lastChild = i;
  }
  return lastChild + 1;
}

function splitValueComment(text: string): { value: string; comment: string } {
  const hash = commentIndex(text);
  return {
    value: (hash !== -1 ? text.slice(0, hash) : text).trim(),
    comment: hash !== -1 ? text.slice(hash).trim() : "",
  };
}

/** Decode a simple YAML scalar (quoted or plain) so keys can be matched. */
export function yamlScalarValue(text: string): string {
  const { value } = splitValueComment(text);
  if (value.startsWith('"') && value.endsWith('"')) {
    try {
      return JSON.parse(value);
    } catch {
      return value.slice(1, -1);
    }
  }
  if (value.startsWith("'") && value.endsWith("'")) {
    return value.slice(1, -1).replace(/''/g, "'");
  }
  return value;
}

/** Return the unquoted trailing comment for a YAML scalar fragment. */
export function yamlTrailingComment(text: string): string {
  return splitValueComment(text).comment;
}

export function findFieldLine(lines: string[], path: string[]): number {
  let depth = 0;
  let sectionIndent = -1;

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;

    const indent = indentOf(line);
    const colon = line.indexOf(":");
    if (colon === -1) continue;
    const key = line.slice(indent, colon).trim();

    if (key === path[depth]) {
      if (depth === path.length - 1) return i;
      depth++;
      sectionIndent = indent;
    } else if (indent <= sectionIndent && depth > 0) {
      depth = 0;
      sectionIndent = -1;
    }
  }
  return -1;
}

export function insertYamlField(yaml: string, path: string[], formattedVal: string): string {
  const lines = yaml.split("\n");

  let ancestorIdx = -1;
  let ancestorDepth = 0;
  for (let depth = path.length - 1; depth >= 1; depth--) {
    const idx = findFieldLine(lines, path.slice(0, depth));
    if (idx !== -1) {
      ancestorIdx = idx;
      ancestorDepth = depth;
      break;
    }
  }

  let indent = 0;
  let insertAt = lines.length;
  let step = 2;
  if (ancestorIdx !== -1) {
    const ancestorIndent = indentOf(lines[ancestorIdx]);
    insertAt = findBlockInsertLine(lines, ancestorIdx, ancestorIndent);
    step = detectIndentStep(lines, ancestorIdx + 1, insertAt, ancestorIndent);
    indent = ancestorIndent + step;
  } else if (lines.length > 0 && isBlank(lines[lines.length - 1])) {
    // Avoid appending after the file's trailing blank line.
    insertAt = lines.length - 1;
  }

  const newLines: string[] = [];
  for (let depth = ancestorDepth; depth < path.length - 1; depth++) {
    newLines.push(" ".repeat(indent) + path[depth] + ":");
    indent += step;
  }
  newLines.push(" ".repeat(indent) + path[path.length - 1] + ": " + formattedVal);

  lines.splice(insertAt, 0, ...newLines);
  return lines.join("\n");
}

export function setYamlScalar(
  yaml: string,
  path: string[],
  newValue: unknown,
  type: YamlScalarType,
): string {
  const lines = yaml.split("\n");
  const formatted = formatScalar(newValue, type);
  const idx = findFieldLine(lines, path);
  if (idx === -1) {
    return insertYamlField(yaml, path, formatted);
  }

  const info = splitKeyLine(lines[idx]);
  if (!info) return yaml;
  lines[idx] = `${info.keyPart} ${formatted}${info.comment}`;
  return lines.join("\n");
}

function rewriteBlockList(
  lines: string[],
  keyIdx: number,
  values: unknown[],
  info: { indent: number; keyPart: string; comment: string },
): string[] {
  const keyIndent = info.indent;
  let blockEnd = keyIdx + 1;
  while (blockEnd < lines.length) {
    const line = lines[blockEnd];
    if (isBlank(line)) {
      blockEnd++;
      continue;
    }
    if (indentOf(line) <= keyIndent) break;
    blockEnd++;
  }

  const block = lines.slice(keyIdx + 1, blockEnd);
  let itemIndent = keyIndent + 2;
  for (const line of block) {
    const match = line.match(/^(\s*)-\s/);
    if (match) {
      itemIndent = match[1].length;
      break;
    }
  }

  const preserved: string[] = [];
  const itemComments: string[] = [];
  for (const line of block) {
    const item = line.match(/^(\s*)-\s*(.*)$/);
    if (item) {
      const hash = commentIndex(item[2]);
      itemComments.push(hash !== -1 ? item[2].slice(hash).trim() : "");
      continue;
    }
    // Keep standalone comments and any unexpected nested content.
    preserved.push(line);
  }

  if (values.length === 0) {
    const comments = preserved
      .filter((line) => !isBlank(line))
      .map((line) => " ".repeat(keyIndent) + line.trim());
    lines.splice(keyIdx, blockEnd - keyIdx, ...comments, `${info.keyPart} []${info.comment}`);
    return lines;
  }

  const rebuilt = [`${info.keyPart}${info.comment}`, ...preserved];
  values.forEach((value, index) => {
    const comment = itemComments[index] ? ` ${itemComments[index]}` : "";
    rebuilt.push(`${" ".repeat(itemIndent)}- ${yamlQuote(value)}${comment}`);
  });
  lines.splice(keyIdx, blockEnd - keyIdx, ...rebuilt);
  return lines;
}

export function setYamlList(yaml: string, path: string[], values: unknown[]): string {
  const lines = yaml.split("\n");
  const flow = "[" + values.map((value) => yamlQuote(value)).join(", ") + "]";
  const idx = findFieldLine(lines, path);
  if (idx === -1) {
    return insertYamlField(yaml, path, flow);
  }

  const info = splitKeyLine(lines[idx]);
  if (!info) return yaml;

  if (info.value !== "") {
    lines[idx] = `${info.keyPart} ${flow}${info.comment}`;
    return lines.join("\n");
  }

  return rewriteBlockList(lines, idx, values, info).join("\n");
}

/** Find the indent used by a block list under `keyIdx`. */
export function blockListIndent(lines: string[], keyIdx: number): number {
  const keyIndent = indentOf(lines[keyIdx]);
  for (let i = keyIdx + 1; i < lines.length; i++) {
    const line = lines[i];
    if (isBlank(line)) continue;
    if (indentOf(line) <= keyIndent) break;
    const match = line.match(/^(\s*)-\s/);
    if (match) return match[1].length;
  }
  return keyIndent + 2;
}

/** Replace a whole block, keeping the key's trailing comment and indentation. */
export function replaceYamlBlock(yaml: string, path: string[], blockLines: string[]): string {
  const lines = yaml.split("\n");
  const idx = findFieldLine(lines, path);
  if (idx === -1) {
    if (blockLines.length === 0) {
      return insertYamlField(yaml, path, "[]");
    }
    // Create the key first so indentation is resolved by the existing tree.
    return replaceYamlBlock(insertYamlField(yaml, path, "[]"), path, blockLines);
  }

  const info = splitKeyLine(lines[idx]);
  if (!info) return yaml;

  const keyIndent = info.indent;
  let blockEnd = idx + 1;
  while (blockEnd < lines.length) {
    const line = lines[blockEnd];
    if (isBlank(line)) {
      blockEnd++;
      continue;
    }
    if (indentOf(line) <= keyIndent) break;
    blockEnd++;
  }

  if (blockLines.length === 0) {
    lines.splice(idx, blockEnd - idx, `${info.keyPart} []${info.comment}`);
    return lines.join("\n");
  }

  const preserved = lines
    .slice(idx + 1, blockEnd)
    .filter((line) => isBlank(line) || isComment(line));
  const replacement = [`${info.keyPart}${info.comment}`, ...preserved, ...blockLines];
  lines.splice(idx, blockEnd - idx, ...replacement);
  return lines.join("\n");
}
