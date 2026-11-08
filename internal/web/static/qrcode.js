// src/utils/qrcode.ts
var EXP = new Uint8Array(256);
var LOG = new Uint8Array(256);
var x = 1;
for (let i = 0; i < 255; i++) {
  EXP[i] = x;
  LOG[x] = i;
  x <<= 1;
  if (x & 256) x ^= 285;
}
EXP[255] = EXP[0];
function gfMul(a, b) {
  if (a === 0 || b === 0) return 0;
  return EXP[(LOG[a] + LOG[b]) % 255];
}
function polyMul(p1, p2) {
  const r = new Uint8Array(p1.length + p2.length - 1);
  for (let i = 0; i < p1.length; i++) {
    for (let j = 0; j < p2.length; j++) {
      r[i + j] ^= gfMul(p1[i], p2[j]);
    }
  }
  return r;
}
function getGenPoly(degree) {
  let g = new Uint8Array([1]);
  for (let i = 0; i < degree; i++) {
    g = polyMul(g, new Uint8Array([1, EXP[i]]));
  }
  return g;
}
function calcEC(dataBlock, ecCount) {
  const gen = getGenPoly(ecCount);
  const work = new Uint8Array(dataBlock.length + ecCount);
  work.set(dataBlock, 0);
  for (let i = 0; i < dataBlock.length; i++) {
    const coef = work[i];
    if (coef !== 0) {
      for (let j = 0; j < gen.length; j++) {
        work[i + j] ^= gfMul(gen[j], coef);
      }
    }
  }
  return work.slice(dataBlock.length);
}
var VERSIONS = [
  null,
  { v: 1, dim: 21, ec: 7, blocks: [{ count: 1, data: 19 }], align: [] },
  { v: 2, dim: 25, ec: 10, blocks: [{ count: 1, data: 34 }], align: [6, 18] },
  { v: 3, dim: 29, ec: 15, blocks: [{ count: 1, data: 55 }], align: [6, 22] },
  { v: 4, dim: 33, ec: 20, blocks: [{ count: 1, data: 80 }], align: [6, 26] },
  { v: 5, dim: 37, ec: 26, blocks: [{ count: 1, data: 108 }], align: [6, 30] },
  { v: 6, dim: 41, ec: 18, blocks: [{ count: 2, data: 68 }], align: [6, 34] },
  { v: 7, dim: 45, ec: 20, blocks: [{ count: 2, data: 78 }], align: [6, 22, 38] },
  { v: 8, dim: 49, ec: 24, blocks: [{ count: 2, data: 97 }], align: [6, 24, 42] },
  { v: 9, dim: 53, ec: 30, blocks: [{ count: 2, data: 116 }], align: [6, 26, 46] },
  { v: 10, dim: 57, ec: 18, blocks: [{ count: 2, data: 68 }, { count: 2, data: 69 }], align: [6, 28, 50] }
];
function selectVersion(byteLen) {
  for (let v = 1; v <= 10; v++) {
    const totalData = VERSIONS[v].blocks.reduce((acc, b) => acc + b.count * b.data, 0);
    const lenBits = v <= 9 ? 8 : 16;
    const reqBits = 4 + lenBits + byteLen * 8 + 4;
    if (Math.ceil(reqBits / 8) <= totalData) {
      return VERSIONS[v];
    }
  }
  return VERSIONS[10];
}
function createQRCode(text) {
  const encoder = new TextEncoder();
  const rawData = encoder.encode(text);
  const vInfo = selectVersion(rawData.length);
  const version = vInfo.v;
  const dim = vInfo.dim;
  const totalData = vInfo.blocks.reduce((acc, b) => acc + b.count * b.data, 0);
  const bits = [];
  function putBits(num, len) {
    for (let i = len - 1; i >= 0; i--) bits.push(num >>> i & 1);
  }
  putBits(4, 4);
  putBits(rawData.length, version <= 9 ? 8 : 16);
  for (const b of rawData) putBits(b, 8);
  const maxBits = totalData * 8;
  const termLen = Math.min(4, maxBits - bits.length);
  putBits(0, termLen);
  while (bits.length % 8 !== 0) bits.push(0);
  const padBytes = [236, 17];
  let padIdx = 0;
  while (bits.length < maxBits) {
    putBits(padBytes[padIdx % 2], 8);
    padIdx++;
  }
  const dataCodewords = new Uint8Array(totalData);
  for (let i = 0; i < totalData; i++) {
    let byte = 0;
    for (let j = 0; j < 8; j++) byte = byte << 1 | bits[i * 8 + j];
    dataCodewords[i] = byte;
  }
  const dataBlocks = [];
  const ecBlocks = [];
  let cwOffset = 0;
  for (const b of vInfo.blocks) {
    for (let i = 0; i < b.count; i++) {
      const dBlock = dataCodewords.slice(cwOffset, cwOffset + b.data);
      cwOffset += b.data;
      dataBlocks.push(dBlock);
      ecBlocks.push(calcEC(dBlock, vInfo.ec));
    }
  }
  const finalCodewords = [];
  const maxBlockLen = Math.max(...dataBlocks.map((b) => b.length));
  for (let i = 0; i < maxBlockLen; i++) {
    for (let b = 0; b < dataBlocks.length; b++) {
      if (i < dataBlocks[b].length) finalCodewords.push(dataBlocks[b][i]);
    }
  }
  for (let i = 0; i < vInfo.ec; i++) {
    for (let b = 0; b < ecBlocks.length; b++) {
      finalCodewords.push(ecBlocks[b][i]);
    }
  }
  const matrix = Array.from({ length: dim }, () => new Int8Array(dim).fill(-1));
  const isFunc = Array.from({ length: dim }, () => new Uint8Array(dim).fill(0));
  function markRegion(x2, y, w, h) {
    for (let r = y; r < y + h; r++) {
      for (let c = x2; c < x2 + w; c++) {
        if (r >= 0 && r < dim && c >= 0 && c < dim) isFunc[r][c] = 1;
      }
    }
  }
  function setFinder(x2, y) {
    for (let r = 0; r < 7; r++) {
      for (let c = 0; c < 7; c++) {
        const isBlack = r === 0 || r === 6 || c === 0 || c === 6 || r >= 2 && r <= 4 && c >= 2 && c <= 4;
        matrix[y + r][x2 + c] = isBlack ? 1 : 0;
      }
    }
    for (let r = -1; r <= 7; r++) {
      for (let c = -1; c <= 7; c++) {
        const nr = y + r, nc = x2 + c;
        if (nr >= 0 && nr < dim && nc >= 0 && nc < dim) {
          if (matrix[nr][nc] === -1) matrix[nr][nc] = 0;
        }
      }
    }
  }
  setFinder(0, 0);
  setFinder(dim - 7, 0);
  setFinder(0, dim - 7);
  markRegion(0, 0, 9, 9);
  markRegion(dim - 8, 0, 8, 9);
  markRegion(0, dim - 8, 9, 8);
  for (let i = 8; i < dim - 8; i++) {
    matrix[6][i] = i % 2 === 0 ? 1 : 0;
    matrix[i][6] = i % 2 === 0 ? 1 : 0;
    isFunc[6][i] = 1;
    isFunc[i][6] = 1;
  }
  for (const cx of vInfo.align) {
    for (const cy of vInfo.align) {
      if (cx === 6 && cy === 6 || cx === 6 && cy === dim - 7 || cx === dim - 7 && cy === 6) continue;
      for (let r = -2; r <= 2; r++) {
        for (let c = -2; c <= 2; c++) {
          const isBlack = Math.abs(r) === 2 || Math.abs(c) === 2 || r === 0 && c === 0;
          matrix[cy + r][cx + c] = isBlack ? 1 : 0;
          isFunc[cy + r][cx + c] = 1;
        }
      }
    }
  }
  matrix[dim - 8][8] = 1;
  isFunc[dim - 8][8] = 1;
  if (version >= 7) {
    markRegion(dim - 11, 0, 3, 6);
    markRegion(0, dim - 11, 6, 3);
    let rem = version << 12;
    for (let i = 17; i >= 12; i--) {
      if (rem >>> i & 1) rem ^= 7973 << i - 12;
    }
    const verBits = version << 12 | rem;
    for (let i = 0; i < 18; i++) {
      const bit = verBits >>> i & 1;
      const r = Math.floor(i / 3), c = dim - 11 + i % 3;
      matrix[r][c] = bit;
      matrix[c][r] = bit;
    }
  }
  const finalBits = [];
  for (const b of finalCodewords) {
    for (let i = 7; i >= 0; i--) finalBits.push(b >>> i & 1);
  }
  let bitIdx = 0;
  let readingUp = true;
  for (let col = dim - 1; col > 0; col -= 2) {
    if (col === 6) col--;
    for (let i = 0; i < dim; i++) {
      const r = readingUp ? dim - 1 - i : i;
      for (let offset = 0; offset < 2; offset++) {
        const c = col - offset;
        if (!isFunc[r][c]) {
          const bit = bitIdx < finalBits.length ? finalBits[bitIdx++] : 0;
          const mask = (r + c) % 2 === 0 ? 1 : 0;
          matrix[r][c] = bit ^ mask;
        }
      }
    }
    readingUp = !readingUp;
  }
  const formatVal = 30660;
  const fBits = [];
  for (let i = 14; i >= 0; i--) fBits.push(formatVal >>> i & 1);
  let fbIdx = 0;
  for (let c = 0; c <= 8; c++) {
    if (c !== 6) matrix[8][c] = fBits[fbIdx++];
  }
  for (let r = 7; r >= 0; r--) {
    if (r !== 6) matrix[r][8] = fBits[fbIdx++];
  }
  fbIdx = 0;
  for (let r = dim - 1; r >= dim - 7; r--) {
    matrix[r][8] = fBits[fbIdx++];
  }
  for (let c = dim - 8; c < dim; c++) {
    matrix[8][c] = fBits[fbIdx++];
  }
  return {
    getModuleCount: () => dim,
    isDark: (r, c) => matrix[r][c] === 1
  };
}
function generateQRCodeSVG(text, size = 220) {
  const qr = createQRCode(text);
  const count = qr.getModuleCount();
  const margin = 3;
  const viewBoxSize = count + margin * 2;
  const pathParts = [];
  for (let r = 0; r < count; r++) {
    for (let c = 0; c < count; c++) {
      if (qr.isDark(r, c)) {
        pathParts.push(`M${c + margin},${r + margin}h1v1h-1z`);
      }
    }
  }
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${viewBoxSize} ${viewBoxSize}" width="${size}" height="${size}" shape-rendering="crispEdges">
    <rect width="${viewBoxSize}" height="${viewBoxSize}" fill="#ffffff"/>
    <path d="${pathParts.join(" ")}" fill="#000000"/>
  </svg>`;
}
export {
  createQRCode,
  generateQRCodeSVG
};
//# sourceMappingURL=qrcode.js.map
