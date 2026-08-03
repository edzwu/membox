/* Miru — minimal ZIP support, ported verbatim from the original app.js.
   Exports use STORE (no compression); imports also read ordinary DEFLATE
   archives when DecompressionStream is available. Pure byte-level code: no
   DOM access, no app state, easy to reason about (or unit test) in isolation. */

export function crc32(bytes) {
  let table = crc32.table;
  if (!table) {
    table = crc32.table = new Uint32Array(256);
    for (let i = 0; i < 256; i++) {
      let c = i;
      for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
      table[i] = c >>> 0;
    }
  }
  let crc = 0xffffffff;
  for (let i = 0; i < bytes.length; i++) crc = table[(crc ^ bytes[i]) & 0xff] ^ (crc >>> 8);
  return (crc ^ 0xffffffff) >>> 0;
}

export function buildZip(files) {
  const encoder = new TextEncoder();
  const chunks = [];
  const central = [];
  let offset = 0;

  files.forEach((file) => {
    const nameBytes = encoder.encode(file.name);
    const crc = crc32(file.data);
    const size = file.data.length;

    const local = new DataView(new ArrayBuffer(30));
    local.setUint32(0, 0x04034b50, true);
    local.setUint16(4, 20, true);
    local.setUint16(6, 0x0800, true); // UTF-8 names
    local.setUint16(8, 0, true);      // method: store
    local.setUint32(14, crc, true);
    local.setUint32(18, size, true);
    local.setUint32(22, size, true);
    local.setUint16(26, nameBytes.length, true);
    local.setUint16(28, 0, true);
    chunks.push(new Uint8Array(local.buffer), nameBytes, file.data);

    const cd = new DataView(new ArrayBuffer(46));
    cd.setUint32(0, 0x02014b50, true);
    cd.setUint16(4, 20, true);
    cd.setUint16(6, 20, true);
    cd.setUint16(8, 0x0800, true);
    cd.setUint32(16, crc, true);
    cd.setUint32(20, size, true);
    cd.setUint32(24, size, true);
    cd.setUint16(28, nameBytes.length, true);
    cd.setUint32(42, offset, true);
    central.push(new Uint8Array(cd.buffer), nameBytes);

    offset += 30 + nameBytes.length + size;
  });

  const centralStart = offset;
  const centralSize = central.reduce((sum, c) => sum + c.length, 0);

  const end = new DataView(new ArrayBuffer(22));
  end.setUint32(0, 0x06054b50, true);
  end.setUint16(8, files.length, true);
  end.setUint16(10, files.length, true);
  end.setUint32(12, centralSize, true);
  end.setUint32(16, centralStart, true);

  const total = centralStart + centralSize + 22;
  const zip = new Uint8Array(total);
  let pos = 0;
  chunks.forEach((c) => { zip.set(c, pos); pos += c.length; });
  central.forEach((c) => { zip.set(c, pos); pos += c.length; });
  zip.set(new Uint8Array(end.buffer), pos);
  return zip;
}

export function zipBasename(name) {
  return name.replace(/\\/g, '/').split('/').filter(Boolean).pop() || '';
}

async function decompressZipEntry(method, bytes) {
  if (method === 0) return new Uint8Array(bytes);
  if (method !== 8 || typeof window.DecompressionStream !== 'function') {
    throw new Error('Unsupported ZIP compression');
  }
  try {
    const stream = new Blob([bytes]).stream()
      .pipeThrough(new window.DecompressionStream('deflate-raw'));
    return new Uint8Array(await new Response(stream).arrayBuffer());
  } catch (err) {
    throw new Error('Could not decompress Miru bundle');
  }
}

export async function readZipEntries(file) {
  if (file.size > 100 * 1024 * 1024) throw new Error('ZIP file is too large');
  const bytes = new Uint8Array(await file.arrayBuffer());
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const minEocd = Math.max(0, bytes.length - 0xffff - 22);
  let eocd = -1;
  for (let i = bytes.length - 22; i >= minEocd; i--) {
    if (view.getUint32(i, true) === 0x06054b50) {
      eocd = i;
      break;
    }
  }
  if (eocd === -1) throw new Error('Invalid ZIP file');

  const entryCount = view.getUint16(eocd + 10, true);
  const centralSize = view.getUint32(eocd + 12, true);
  const centralOffset = view.getUint32(eocd + 16, true);
  if (entryCount > 100 || centralOffset + centralSize > bytes.length) {
    throw new Error('Invalid ZIP directory');
  }

  const decoder = new TextDecoder('utf-8');
  const entries = [];
  let totalSize = 0;
  let offset = centralOffset;
  for (let i = 0; i < entryCount; i++) {
    if (offset + 46 > bytes.length || view.getUint32(offset, true) !== 0x02014b50) {
      throw new Error('Invalid ZIP entry');
    }
    const flags = view.getUint16(offset + 8, true);
    const method = view.getUint16(offset + 10, true);
    const expectedCrc = view.getUint32(offset + 16, true);
    const compressedSize = view.getUint32(offset + 20, true);
    const uncompressedSize = view.getUint32(offset + 24, true);
    const nameLength = view.getUint16(offset + 28, true);
    const extraLength = view.getUint16(offset + 30, true);
    const commentLength = view.getUint16(offset + 32, true);
    const localOffset = view.getUint32(offset + 42, true);
    if ((flags & 1) || compressedSize === 0xffffffff || uncompressedSize === 0xffffffff) {
      throw new Error('Encrypted or ZIP64 bundles are unsupported');
    }
    if (offset + 46 + nameLength + extraLength + commentLength > bytes.length ||
        localOffset + 30 > bytes.length || view.getUint32(localOffset, true) !== 0x04034b50) {
      throw new Error('Invalid ZIP entry bounds');
    }

    const name = decoder.decode(bytes.subarray(offset + 46, offset + 46 + nameLength));
    const localNameLength = view.getUint16(localOffset + 26, true);
    const localExtraLength = view.getUint16(localOffset + 28, true);
    const dataStart = localOffset + 30 + localNameLength + localExtraLength;
    const dataEnd = dataStart + compressedSize;
    if (dataEnd > bytes.length) throw new Error('Invalid ZIP entry data');

    if (!name.endsWith('/')) {
      totalSize += uncompressedSize;
      if (totalSize > 50 * 1024 * 1024) throw new Error('ZIP contents are too large');
      const data = await decompressZipEntry(method, bytes.subarray(dataStart, dataEnd));
      if (data.length !== uncompressedSize || crc32(data) !== expectedCrc) {
        throw new Error('Corrupt ZIP entry');
      }
      entries.push({ name, data });
    }
    offset += 46 + nameLength + extraLength + commentLength;
  }
  return entries;
}
