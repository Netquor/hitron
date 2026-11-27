#!/usr/bin/env python3
"""Render hitron-cli sessions to PNG and GIF with no screen recorder.

Runs the real binary under a pty against replay.py, plays a scripted list of
keystrokes, keeps the terminal in a pyte screen model, and draws frames with
Pillow. Needs: pyte, pillow, DejaVu Sans Mono.

    python3 render.py --binary ./hitron-cli --out ../../site/assets/cli
"""
import codecs, argparse, fcntl, os, pty, select, signal, struct, subprocess, sys, tempfile, termios, time
import pyte
from PIL import Image, ImageDraw, ImageFilter, ImageFont

COLS, ROWS = 108, 44
FONT_DIR = os.environ.get("HITRON_FONT_DIR", "/usr/share/fonts/truetype/dejavu")
FONT = os.path.join(FONT_DIR, "DejaVuSansMono.ttf")
FONT_SYM = os.environ.get("HITRON_FONT_SYM", "/usr/share/fonts/truetype/freefont/FreeMono.ttf")
if not os.path.exists(FONT_SYM):
    FONT_SYM = os.path.join(FONT_DIR, "DejaVuSans.ttf")  # carries the braille block
FONT_B = os.path.join(FONT_DIR, "DejaVuSansMono-Bold.ttf")
FONT_UI = os.path.join(FONT_DIR, "DejaVuSans.ttf")
SIZE, CW, CH, PAD = 16, 10, 20, 14
DEFAULT_BG, DEFAULT_FG = (10, 11, 20), (214, 218, 232)
NAMED = {"black": (0, 0, 0), "red": (205, 49, 49), "green": (13, 188, 121), "brown": (229, 229, 16),
         "blue": (36, 114, 200), "magenta": (188, 63, 188), "cyan": (17, 168, 205), "white": (229, 229, 229)}
BRIGHT = {"brightblack": (102, 102, 102), "brightred": (241, 76, 76), "brightgreen": (35, 209, 139),
          "brightbrown": (245, 245, 67), "brightblue": (59, 142, 234), "brightmagenta": (214, 112, 214),
          "brightcyan": (41, 184, 219), "brightwhite": (255, 255, 255)}


def xterm256(n):
    if n < 16:
        base = list(NAMED.values()) + list(BRIGHT.values())
        return base[n]
    if n >= 232:
        v = 8 + (n - 232) * 10
        return (v, v, v)
    n -= 16
    lv = [0, 95, 135, 175, 215, 255]
    return (lv[n // 36], lv[n // 6 % 6], lv[n % 6])


def color(c, default):
    if c == "default":
        return default
    if c in NAMED:
        return NAMED[c]
    if c in BRIGHT:
        return BRIGHT[c]
    if len(c) == 6:
        try:
            return tuple(int(c[i:i + 2], 16) for i in (0, 2, 4))
        except ValueError:
            pass
    return default


class Painter:
    def __init__(self):
        self.f = ImageFont.truetype(FONT, SIZE)
        self.fb = ImageFont.truetype(FONT_B, SIZE)
        self.sym = ImageFont.truetype(FONT_SYM, SIZE)
        self.w = COLS * CW + PAD * 2
        self.h = ROWS * CH + PAD * 2

    def chrome(self, content):
        """Wrap a frame in a macOS-style window (RGBA, transparent rounded corners)."""
        bar, rad, S = 40, 12, 4
        w, h = content.width, content.height + bar
        win = Image.new("RGBA", (w * S, h * S), (0, 0, 0, 0))
        d = ImageDraw.Draw(win)
        d.rounded_rectangle([0, 0, w * S - 1, h * S - 1], rad * S, fill=(30, 31, 38, 255))
        d.rounded_rectangle([0, 0, w * S - 1, (bar + rad) * S], rad * S, fill=(46, 47, 55, 255))
        d.rectangle([0, bar * S, w * S, (bar + rad) * S], fill=(30, 31, 38, 255))
        d.line([0, bar * S - 1, w * S, bar * S - 1], fill=(14, 14, 18, 255), width=S)
        for i, (hi, mid, rim) in enumerate([((255, 141, 130), (255, 95, 86), (224, 62, 54)),
                                           ((255, 216, 112), (255, 189, 46), (214, 150, 20)),
                                           ((120, 232, 130), (39, 201, 63), (28, 150, 44))]):
            cx, cy, rr = (20 + i * 22) * S, (bar // 2) * S, 6 * S
            d.ellipse([cx - rr, cy - rr, cx + rr, cy + rr], fill=rim)
            d.ellipse([cx - rr + S, cy - rr + S, cx + rr - S, cy + rr - S], fill=mid)
            d.ellipse([cx - rr + 2 * S, cy - rr + S, cx + rr - 2 * S, cy], fill=hi)
        win = win.resize((w, h), Image.LANCZOS)
        d = ImageDraw.Draw(win)
        f = ImageFont.truetype(FONT_UI, 13)
        title = "hitron-cli \u2014 monitor \u2014 %d\u00d7%d" % (COLS, ROWS)
        d.text(((w - d.textlength(title, font=f)) / 2, bar // 2 - 8), title, font=f, fill=(165, 167, 180, 255))
        mask = Image.new("L", (w * S, h * S), 0)
        ImageDraw.Draw(mask).rounded_rectangle([0, bar * S, w * S - 1, h * S - 1], rad * S, fill=255)
        ImageDraw.Draw(mask).rectangle([0, bar * S, w * S, (bar + rad) * S], fill=255)
        mask = mask.resize((w, h), Image.LANCZOS).crop((0, bar, w, h))
        win.paste(content.convert("RGBA"), (0, bar), mask)
        return win

    rows = None

    def draw(self, screen):
        img = self._draw(screen)
        if self.rows:
            a, b = self.rows
            img = img.crop((0, a * CH + (PAD if a else 0), img.width, PAD + b * CH))
        return self.chrome(img)

    def _draw(self, screen):
        img = Image.new("RGB", (self.w, self.h), DEFAULT_BG)
        d = ImageDraw.Draw(img)
        for y in range(ROWS):
            row = screen.buffer[y]
            x = 0
            while x < COLS:
                ch = row[x]
                fg, bg = color(ch.fg, DEFAULT_FG), color(ch.bg, DEFAULT_BG)
                if ch.reverse:
                    fg, bg = bg, fg
                px, py = PAD + x * CW, PAD + y * CH
                if bg != DEFAULT_BG:
                    d.rectangle([px, py, px + CW - 1, py + CH - 1], fill=bg)
                if ch.data.strip():
                    d.text((px, py + 1), ch.data, font=self.sym if 0x2800 <= ord(ch.data[0]) <= 0x28FF else (self.fb if ch.bold else self.f), fill=fg)
                x += 1
        return img


class Session:
    def __init__(self, binary, host, home, args):
        self.painter = Painter()
        self.screen = pyte.Screen(COLS, ROWS)
        self.stream = pyte.Stream(self.screen)
        self.dec = codecs.getincrementaldecoder('utf8')('replace')
        self.frames = []
        pid, fd = pty.fork()
        if pid == 0:
            env = dict(os.environ, HTTP_PROXY=os.environ.get("DEMO_PROXY", ""), http_proxy=os.environ.get("DEMO_PROXY", ""), NO_PROXY="", no_proxy="", TERM="xterm-256color", COLORTERM="truecolor", HOME=home, HITRON_THEME="")
            os.execve(binary, [binary, "monitor", "--host", host] + args, env)
        fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))
        self.pid, self.fd = pid, fd

    def pump(self, seconds, fps=5, record=True):
        end, step = time.time() + seconds, 1.0 / fps
        nxt = time.time()
        while time.time() < end:
            r, _, _ = select.select([self.fd], [], [], 0.05)
            if r:
                try:
                    chunk = os.read(self.fd, 1 << 16)
                except OSError:
                    return
                if b"\x1b]11;?" in chunk:
                    os.write(self.fd, b"\x1b]11;rgb:0a0a/0b0b/1414\x1b\\")
                if b"\x1b[6n" in chunk:
                    os.write(self.fd, b"\x1b[1;1R")
                self.stream.feed(self.dec.decode(chunk))
            if record and time.time() >= nxt:
                self.frames.append(self.painter.draw(self.screen))
                nxt += step

    def key(self, data, wait=0.3):
        for b in data:
            os.write(self.fd, bytes([b]))
            time.sleep(0.04)
        self.pump(wait, record=False)

    def close(self):
        try:
            os.kill(self.pid, signal.SIGKILL)
            os.waitpid(self.pid, 0)
        except OSError:
            pass


LIGHTS = [(255, 141, 130), (255, 95, 86), (224, 62, 54), (255, 216, 112), (255, 189, 46),
          (214, 150, 20), (120, 232, 130), (39, 201, 63), (28, 150, 44), (46, 47, 55), (30, 31, 38)]


def palette_for(sample):
    base = sample.convert("RGB").quantize(colors=244, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE)
    flat = base.getpalette()[: 244 * 3]
    for c in LIGHTS:
        flat += list(c)
    flat += [0, 0, 0] * (256 - len(flat) // 3)
    pal = Image.new("P", (1, 1))
    pal.putpalette(flat)
    return pal


def to_indexed(img, pal):
    out = img.convert("RGB").quantize(palette=pal, dither=Image.Dither.NONE)
    clear = img.getchannel("A").point(lambda a: 255 if a < 128 else 0)
    out.paste(255, mask=clear)
    return out


def save_png(img, path):
    to_indexed(img, palette_for(img)).save(path, transparency=255, optimize=True)


def save_gif(frames, path, fps=5):
    if not frames:
        return
    pal = palette_for(frames[len(frames) // 2])
    out = [to_indexed(f, pal) for f in frames]
    out[0].save(path, save_all=True, append_images=out[1:], duration=int(1000 / fps), loop=0,
                optimize=False, disposal=1, transparency=255)


def scenario(name, binary, host, out, extra=None):
    home = tempfile.mkdtemp()
    s = Session(binary, host, home, extra or [])
    s.painter.rows = {"analyzer": (0, 32), "reboot": (10, 33)}.get(name)
    shots = {}
    try:
        s.pump(5, record=False)  # let the history build and the layout settle
        if name == "summary":
            s.pump(1)
            shots["summary"] = s.painter.draw(s.screen)
            full = s.painter._draw(s.screen)
            top = PAD + 27 * CH
            shots["codewords"] = s.painter.chrome(full.crop((0, top, full.width, full.height - PAD - CH)))
            s.pump(24)
        elif name == "themes":
            for i, theme in enumerate(["neon", "synthwave", "dracula", "nord", "gruvbox", "matrix"]):
                s.pump(2.2)
                shots["theme-" + theme] = s.painter.draw(s.screen)
                s.key(b"t")
            s.pump(1.5)
        elif name == "analyzer":
            s.key(b"2", 2)
            s.pump(4)
            shots["spectrum"] = s.painter.draw(s.screen)
            for _ in range(3):
                s.key(b"]", 0.2); s.pump(2.5)
            s.key(b"3", 2)
            s.pump(3)
            shots["constellation"] = s.painter.draw(s.screen)
            for _ in range(3):
                s.key(b"]", 0.2); s.pump(2.5)
            s.key(b"1", 1); s.pump(2)
        elif name == "reboot":
            s.pump(1.5)
            s.key(b"x", 1.2); s.pump(2)
            shots["reboot"] = s.painter.draw(s.screen)
            for ch in b"reboot":
                s.key(bytes([ch]), 0.35); s.pump(0.3)
            s.pump(1.5)
            s.key(b"\x1b", 0.5); s.pump(2.5)   # cancel: the demo never confirms
        elif name == "help":
            s.key(b"h", 1); s.pump(1)
            shots["help"] = s.painter.draw(s.screen)
        s.key(b"q", 0.2)
    finally:
        s.close()
    os.makedirs(out, exist_ok=True)
    for k, img in shots.items():
        save_png(img, os.path.join(out, k + ".png"))
    if s.frames:
        save_gif(s.frames, os.path.join(out, name + ".gif"))
    return len(s.frames), list(shots)


if __name__ == "__main__":
    p = argparse.ArgumentParser()
    p.add_argument("--binary", default="./hitron-cli")
    p.add_argument("--host", default="127.0.0.1:8099")
    p.add_argument("--out", default="out")
    p.add_argument("--only", default="")
    a = p.parse_args()
    names = [n for n in ("summary", "themes", "analyzer", "reboot", "help") if not a.only or n in a.only.split(",")]
    for n in names:
        print(n, scenario(n, os.path.abspath(a.binary), a.host, a.out), flush=True)
