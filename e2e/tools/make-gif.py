#!/usr/bin/env python3
"""Puts the frames that record-guide-gifs.spec.js saved into one animated GIF, with nothing but the standard library.

    python3 make-gif.py /tmp/guide-gifs/slash ../../src/resources/images/genie/guide-slash.gif

The directory holds 00.png, 01.png, ... and frames.json ([{"file": "00.png", "ms": 900}, ...]). The pictures are 8-bit PNGs that the browser
saved. The colours are cut to 255 (median cut, no dithering, which suits flat screens), and each frame after the first keeps only the
rectangle that changed, with the pixels that did not change left transparent, which keeps the file small.
"""
import json
import os
import struct
import sys
import zlib


def read_png(path):
    data = open(path, "rb").read()
    assert data[:8] == b"\x89PNG\r\n\x1a\n", "not a PNG: " + path
    pos, idat = 8, b""
    w = h = bd = ct = il = None
    while pos < len(data):
        (ln,) = struct.unpack(">I", data[pos : pos + 4])
        typ, body = data[pos + 4 : pos + 8], data[pos + 8 : pos + 8 + ln]
        pos += 12 + ln
        if typ == b"IHDR":
            w, h, bd, ct, _, _, il = struct.unpack(">IIBBBBB", body)
        elif typ == b"IDAT":
            idat += body
        elif typ == b"IEND":
            break
    assert bd == 8 and il == 0 and ct in (2, 6), "needs an 8-bit RGB or RGBA PNG without interlacing"
    bpp = 3 if ct == 2 else 4
    raw = zlib.decompress(idat)
    stride = w * bpp
    rows, prev, p = [], bytearray(stride), 0
    for _ in range(h):
        f = raw[p]
        p += 1
        line = bytearray(raw[p : p + stride])
        p += stride
        if f == 1:
            for i in range(bpp, stride):
                line[i] = (line[i] + line[i - bpp]) & 255
        elif f == 2:
            for i in range(stride):
                line[i] = (line[i] + prev[i]) & 255
        elif f == 3:
            for i in range(stride):
                left = line[i - bpp] if i >= bpp else 0
                line[i] = (line[i] + ((left + prev[i]) >> 1)) & 255
        elif f == 4:
            for i in range(stride):
                a = line[i - bpp] if i >= bpp else 0
                b = prev[i]
                c = prev[i - bpp] if i >= bpp else 0
                pa, pb, pc = abs(b - c), abs(a - c), abs(a + b - 2 * c)
                pr = a if (pa <= pb and pa <= pc) else (b if pb <= pc else c)
                line[i] = (line[i] + pr) & 255
        rows.append(line)
        prev = line
    pix = []
    for line in rows:
        pix.append([(line[i] << 16) | (line[i + 1] << 8) | line[i + 2] for i in range(0, stride, bpp)])
    return w, h, pix


def median_cut(hist, n):
    """hist: {packed colour: count}. Returns ({colour: index}, [(r, g, b)])."""
    boxes = [list(hist.items())]
    def span(box):
        best = (0, 0)
        for ch, shift in enumerate((16, 8, 0)):
            vals = [(c >> shift) & 255 for c, _ in box]
            best = max(best, (max(vals) - min(vals), ch))
        return best
    while len(boxes) < n:
        cand = [(span(b)[0] * sum(k for _, k in b), i) for i, b in enumerate(boxes) if len(b) > 1]
        if not cand or max(cand)[0] == 0:
            break
        _, i = max(cand)
        box = boxes.pop(i)
        _, ch = span(box)
        shift = (16, 8, 0)[ch]
        box.sort(key=lambda t: (t[0] >> shift) & 255)
        half, acc = sum(k for _, k in box) / 2, 0
        cut = 1
        for j, (_, k) in enumerate(box):
            acc += k
            if acc >= half:
                cut = max(1, min(j + 1, len(box) - 1))
                break
        boxes.append(box[:cut])
        boxes.append(box[cut:])
    index, pal = {}, []
    for bi, box in enumerate(boxes):
        tot = sum(k for _, k in box)
        r = sum(((c >> 16) & 255) * k for c, k in box) / tot
        g = sum(((c >> 8) & 255) * k for c, k in box) / tot
        b = sum((c & 255) * k for c, k in box) / tot
        pal.append((int(r + 0.5), int(g + 0.5), int(b + 0.5)))
        for c, _ in box:
            index[c] = bi
    return index, pal


def lzw(indices, min_code=8):
    clear, eoi = 1 << min_code, (1 << min_code) + 1
    out, buf, bits = bytearray(), 0, 0
    size, nxt, table = min_code + 1, eoi + 1, {}

    def emit(code):
        nonlocal buf, bits
        buf |= code << bits
        bits += size
        while bits >= 8:
            out.append(buf & 255)
            buf >>= 8
            bits -= 8

    emit(clear)
    prefix = indices[0]
    for b in indices[1:]:
        key = (prefix << 8) | b
        c = table.get(key)
        if c is not None:
            prefix = c
            continue
        emit(prefix)
        if nxt > (1 << size) - 1 and size < 12:
            size += 1
        if nxt < 4096:
            table[key] = nxt
            nxt += 1
        else:
            emit(clear)
            table.clear()
            size, nxt = min_code + 1, eoi + 1
        prefix = b
    emit(prefix)
    emit(eoi)
    if bits:
        out.append(buf & 255)
    return bytes(out)


def blocks(data):
    out = bytearray()
    for i in range(0, len(data), 255):
        chunk = data[i : i + 255]
        out.append(len(chunk))
        out += chunk
    out.append(0)
    return bytes(out)


def main(src, dest):
    meta = json.load(open(os.path.join(src, "frames.json")))
    frames = [read_png(os.path.join(src, m["file"])) for m in meta]
    w, h = frames[0][0], frames[0][1]
    hist = {}
    for fw, fh, pix in frames:
        assert (fw, fh) == (w, h), "the frames differ in size"
        for row in pix:
            for c in row:
                hist[c] = hist.get(c, 0) + 1
    index, pal = median_cut(hist, 255)
    TRANSPARENT = 255
    pal += [(0, 0, 0)] * (256 - len(pal))
    idx = [[bytes(index[c] for c in row) for row in pix] for _, _, pix in frames]
    out = bytearray(b"GIF89a" + struct.pack("<HHBBB", w, h, 0xF7, 0, 0))
    for r, g, b in pal:
        out += bytes((r, g, b))
    out += b"\x21\xff\x0bNETSCAPE2.0\x03\x01\x00\x00\x00"  # loop for ever
    for n, m in enumerate(meta):
        cur = idx[n]
        x0, y0, x1, y1 = 0, 0, w, h
        if n > 0:
            prev = idx[n - 1]
            xs, ys = [], []
            for y in range(h):
                if cur[y] != prev[y]:
                    ys.append(y)
                    a, b = cur[y], prev[y]
                    xs.append(next(i for i in range(w) if a[i] != b[i]))
                    xs.append(next(i for i in range(w - 1, -1, -1) if a[i] != b[i]))
            if ys:
                x0, x1, y0, y1 = min(xs), max(xs) + 1, min(ys), max(ys) + 1
            else:
                x0, y0, x1, y1 = 0, 0, 1, 1
        sub = bytearray()
        for y in range(y0, y1):
            row = cur[y][x0:x1]
            if n > 0:
                p = idx[n - 1][y][x0:x1]
                row = bytes(TRANSPARENT if a == b else a for a, b in zip(row, p))
            sub += row
        delay = max(2, int(m["ms"] / 10))
        flags = (1 << 2) | (1 if n > 0 else 0)  # disposal 1: leave the frame as it is; a transparent colour from the second frame on
        out += b"\x21\xf9\x04" + bytes((flags,)) + struct.pack("<H", delay) + bytes((TRANSPARENT,)) + b"\x00"
        out += b"\x2c" + struct.pack("<HHHHB", x0, y0, x1 - x0, y1 - y0, 0)
        out += b"\x08" + blocks(lzw(bytes(sub)))
    out += b"\x3b"
    open(dest, "wb").write(out)
    print("%s: %d frames, %dx%d, %d KB" % (dest, len(meta), w, h, len(out) // 1024))


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(sys.argv[1], sys.argv[2])
