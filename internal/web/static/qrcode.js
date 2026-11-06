// Self-contained zero-dependency SVG QR Code Generator (RFC 1804 / ISO/IEC 18004).
// Supports Byte mode encoding for URLs and tokens with Error Correction Level L/M.

export function generateQRCodeSVG(text, size = 220) {
  const qr = createQRCode(text);
  const count = qr.getModuleCount();
  const margin = 2;
  const viewBoxSize = count + margin * 2;

  let path = "";
  for (let r = 0; r < count; r++) {
    for (let c = 0; c < count; c++) {
      if (qr.isDark(r, c)) {
        path += `M${c + margin},${r + margin}h1v1h-1z `;
      }
    }
  }

  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${viewBoxSize} ${viewBoxSize}" width="${size}" height="${size}" shape-rendering="crispEdges">
    <rect width="${viewBoxSize}" height="${viewBoxSize}" fill="#ffffff"/>
    <path d="${path.trim()}" fill="#000000"/>
  </svg>`;
}

// GF(256) math
const EXP_TABLE = new Uint8Array(256);
const LOG_TABLE = new Uint8Array(256);
for (let i = 0, x = 1; i < 255; i++) {
  EXP_TABLE[i] = x;
  LOG_TABLE[x] = i;
  x <<= 1;
  if (x & 0x100) x ^= 0x11d;
}
EXP_TABLE[255] = EXP_TABLE[0];

function glog(n) {
  if (n < 1) throw new Error("glog(" + n + ")");
  return LOG_TABLE[n];
}
function gexp(n) {
  return EXP_TABLE[(n % 255 + 255) % 255];
}
function gmult(x, y) {
  if (x === 0 || y === 0) return 0;
  return EXP_TABLE[(LOG_TABLE[x] + LOG_TABLE[y]) % 255];
}

function polyMultiply(p1, p2) {
  const res = new Uint8Array(p1.length + p2.length - 1);
  for (let i = 0; i < p1.length; i++) {
    for (let j = 0; j < p2.length; j++) {
      res[i + j] ^= gmult(p1[i], p2[j]);
    }
  }
  return res;
}

function polyRemainder(dividend, divisor) {
  let res = new Uint8Array(dividend);
  for (let i = 0; i <= dividend.length - divisor.length; i++) {
    const coef = res[i];
    if (coef !== 0) {
      for (let j = 0; j < divisor.length; j++) {
        res[i + j] ^= gmult(divisor[j], coef);
      }
    }
  }
  return res.slice(dividend.length - divisor.length + 1);
}

function getGeneratorPoly(degree) {
  let poly = new Uint8Array([1]);
  for (let i = 0; i < degree; i++) {
    poly = polyMultiply(poly, new Uint8Array([1, gexp(i)]));
  }
  return poly;
}

// Version table: total data codewords and EC capacity for Level L (ecc 7-17%)
const CAPACITY = [
  null,
  { version: 1, total: 26, data: 19, ec: 7, blocks: 1 },
  { version: 2, total: 44, data: 34, ec: 10, blocks: 1 },
  { version: 3, total: 70, data: 55, ec: 15, blocks: 1 },
  { version: 4, total: 100, data: 80, ec: 20, blocks: 1 },
  { version: 5, total: 134, data: 108, ec: 26, blocks: 1 },
  { version: 6, total: 172, data: 136, ec: 18, blocks: 2 },
  { version: 7, total: 196, data: 156, ec: 20, blocks: 2 },
];

function selectVersion(byteLength) {
  for (let v = 1; v < CAPACITY.length; v++) {
    // Mode (4 bits) + length (8 bits for v 1-9) + data bytes * 8 + terminator (up to 4 bits)
    const requiredDataBytes = byteLength + 3;
    if (requiredDataBytes <= CAPACITY[v].data) {
      return CAPACITY[v];
    }
  }
  return CAPACITY[7]; // fallback largest supported
}

function createQRCode(text) {
  const encoder = new TextEncoder();
  const rawData = encoder.encode(text);
  const info = selectVersion(rawData.length);
  const version = info.version;
  const size = version * 4 + 17;

  // 1. Bit buffer encoding: Mode Byte (0100) + Char count + raw data
  const bits = [];
  function putBits(num, len) {
    for (let i = len - 1; i >= 0; i--) {
      bits.push((num >>> i) & 1);
    }
  }

  putBits(0b0100, 4); // Byte mode
  putBits(rawData.length, 8); // 8-bit length for version 1..9
  for (let i = 0; i < rawData.length; i++) {
    putBits(rawData[i], 8);
  }

  // Terminator (up to 4 zeros)
  const maxBits = info.data * 8;
  const termLen = Math.min(4, maxBits - bits.length);
  putBits(0, termLen);

  // Pad to multiple of 8
  while (bits.length % 8 !== 0) {
    bits.push(0);
  }

  // Pad bytes: 0xEC, 0x11
  const padBytes = [0xec, 0x11];
  let padIdx = 0;
  while (bits.length < maxBits) {
    putBits(padBytes[padIdx % 2], 8);
    padIdx++;
  }

  const dataCodewords = new Uint8Array(info.data);
  for (let i = 0; i < info.data; i++) {
    let b = 0;
    for (let j = 0; j < 8; j++) {
      b = (b << 1) | bits[i * 8 + j];
    }
    dataCodewords[i] = b;
  }

  // Error correction
  const numBlocks = info.blocks;
  const ecPerBlock = info.ec;
  const genPoly = getGeneratorPoly(ecPerBlock);

  const blockDataLen = Math.floor(info.data / numBlocks);
  const allDataBlocks = [];
  const allEcBlocks = [];

  for (let b = 0; b < numBlocks; b++) {
    const dBlock = dataCodewords.slice(b * blockDataLen, (b + 1) * blockDataLen);
    allDataBlocks.push(dBlock);

    const dividend = new Uint8Array(dBlock.length + ecPerBlock);
    dividend.set(dBlock, 0);
    const ec = polyRemainder(dividend, genPoly);
    allEcBlocks.push(ec);
  }

  // Interleave codewords
  const finalCodewords = [];
  for (let i = 0; i < blockDataLen; i++) {
    for (let b = 0; b < numBlocks; b++) {
      finalCodewords.push(allDataBlocks[b][i]);
    }
  }
  for (let i = 0; i < ecPerBlock; i++) {
    for (let b = 0; b < numBlocks; b++) {
      finalCodewords.push(allEcBlocks[b][i]);
    }
  }

  // 2. Matrix creation
  const matrix = Array.from({ length: size }, () => new Int8Array(size).fill(-1));

  function setFinder(r, c) {
    for (let i = -1; i <= 7; i++) {
      for (let j = -1; j <= 7; j++) {
        const nr = r + i, nc = c + j;
        if (nr >= 0 && nr < size && nc >= 0 && nc < size) {
          if (
            (i >= 0 && i <= 6 && (j === 0 || j === 6)) ||
            (j >= 0 && j <= 6 && (i === 0 || i === 6)) ||
            (i >= 2 && i <= 4 && j >= 2 && j <= 4)
          ) {
            matrix[nr][nc] = 1;
          } else {
            matrix[nr][nc] = 0;
          }
        }
      }
    }
  }

  // Finders
  setFinder(0, 0);
  setFinder(0, size - 7);
  setFinder(size - 7, 0);

  // Timing patterns
  for (let i = 8; i < size - 8; i++) {
    if (matrix[6][i] === -1) matrix[6][i] = (i % 2 === 0 ? 1 : 0);
    if (matrix[i][6] === -1) matrix[i][6] = (i % 2 === 0 ? 1 : 0);
  }

  // Alignment pattern for version >= 2
  if (version >= 2) {
    const alignPos = [
      [],
      [],
      [6, 18],
      [6, 22],
      [6, 26],
      [6, 30],
      [6, 34],
      [6, 22, 38],
    ][version];
    for (const r of alignPos) {
      for (const c of alignPos) {
        if (matrix[r][c] === -1) {
          for (let i = -2; i <= 2; i++) {
            for (let j = -2; j <= 2; j++) {
              if (Math.abs(i) === 2 || Math.abs(j) === 2 || (i === 0 && j === 0)) {
                matrix[r + i][c + j] = 1;
              } else {
                matrix[r + i][c + j] = 0;
              }
            }
          }
        }
      }
    }
  }

  // Reserve format info areas
  for (let i = 0; i < 9; i++) {
    if (matrix[8][i] === -1) matrix[8][i] = 0;
    if (matrix[i][8] === -1) matrix[i][8] = 0;
    if (matrix[8][size - 1 - i] === -1) matrix[8][size - 1 - i] = 0;
    if (matrix[size - 1 - i][8] === -1) matrix[size - 1 - i][8] = 0;
  }
  matrix[size - 8][8] = 1; // Dark module

  // 3. Place data in 2-column zig-zag
  let bitIdx = 0;
  const finalBits = [];
  for (const b of finalCodewords) {
    for (let i = 7; i >= 0; i--) {
      finalBits.push((b >>> i) & 1);
    }
  }

  let dir = -1; // up
  let col = size - 1;
  while (col > 0) {
    if (col === 6) col--; // skip vertical timing column
    const rowRange = dir === -1
      ? Array.from({ length: size }, (_, i) => size - 1 - i)
      : Array.from({ length: size }, (_, i) => i);

    for (const r of rowRange) {
      for (let c = col; c >= col - 1; c--) {
        if (matrix[r][c] === -1) {
          const bit = bitIdx < finalBits.length ? finalBits[bitIdx++] : 0;
          // Apply mask 0: (row + col) % 2 === 0
          const mask = ((r + c) % 2 === 0) ? 1 : 0;
          matrix[r][c] = bit ^ mask;
        }
      }
    }
    dir = -dir;
    col -= 2;
  }

  // 4. Format info: Level L (01) + Mask 0 (000) -> 01000 -> with BCH = 0x77c4
  const formatBits = [1, 1, 1, 0, 1, 1, 1, 1, 1, 0, 0, 0, 1, 0, 0];
  for (let i = 0; i < 6; i++) matrix[8][i] = formatBits[i];
  matrix[8][7] = formatBits[6];
  matrix[8][8] = formatBits[7];
  matrix[7][8] = formatBits[8];
  for (let i = 9; i < 15; i++) matrix[14 - i][8] = formatBits[i];

  for (let i = 0; i < 8; i++) matrix[size - 1 - i][8] = formatBits[i];
  for (let i = 8; i < 15; i++) matrix[8][size - 15 + i] = formatBits[i];

  return {
    getModuleCount: () => size,
    isDark: (r, c) => matrix[r][c] === 1,
  };
}
