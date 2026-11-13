// What a demo video shows at a moment: the camera on each picture, the
// caption, the ring around what a scene is about and the cursor. A pure
// function of the time, so record.py can draw frames in any order and a
// video drawn twice is the same video. stage.html draws what this says;
// timeline.test.mjs tests it under node.

export const W = 1920;
export const H = 1080;
// The picture is framed above the caption band.
export const TOP = 70;
export const BOTTOM = 210;
export const ANCHOR_Y = (TOP + H - BOTTOM) / 2;
// Screen pixels per picture pixel, at most: the desktop stills are 1x, and
// past this their text goes soft.
export const MAX_ZOOM = 1.9;
export const MOVE = 0.9; // seconds the camera takes from one focus to the next
export const FADE = 0.5; // seconds a new picture takes to come in
const DRIFT = 0.03; // the slow push-in while a scene holds

export const ease = (x) => {
  x = Math.min(1, Math.max(0, x));
  return x < 0.5 ? 4 * x * x * x : 1 - Math.pow(-2 * x + 2, 3) / 2;
};
const lerp = (a, b, e) => a + (b - a) * e;
const ramp = (t, start, len) => Math.min(1, Math.max(0, (t - start) / len));

// When each scene starts, and how long the video runs.
export function starts(video) {
  const at = [];
  let t = 0;
  for (const s of video.scenes) {
    at.push(t);
    t += s.hold;
  }
  return { at, total: t };
}

// The scene playing at t, and how far into it. Past the end is the last
// frame, before the start the first.
export function locate(video, t) {
  const { at, total } = starts(video);
  t = Math.min(Math.max(t, 0), total - 1e-6);
  let i = at.length - 1;
  while (i > 0 && at[i] > t) i--;
  return { i, local: t - at[i], scene: video.scenes[i] };
}

// The camera that frames a rectangle of a picture: the scale, and the
// picture point put at the anchor (the middle, above the captions).
export function frame(rect, size) {
  const [x, y, w, h] = rect || [0, 0, size[0], size[1]];
  const fit = Math.min((0.88 * W) / w, (H - TOP - BOTTOM) / h);
  const whole = Math.min((0.88 * W) / size[0], (H - TOP - BOTTOM) / size[1]);
  return { scale: Math.max(whole, Math.min(fit, MAX_ZOOM)), cx: x + w / 2, cy: y + h / 2 };
}

const samePicture = (a, b) => a && b && a.kind === "window" && b.kind === "window" && a.image === b.image;

// The camera on scene i, local seconds in. A scene on the picture before
// moves from where that one ended; a new picture starts on its own focus.
export function camera(video, sizes, i, local) {
  const s = video.scenes[i];
  const target = frame(s.focus, sizes[s.image]);
  let from = target;
  if (i > 0 && samePicture(video.scenes[i - 1], s)) {
    from = camera(video, sizes, i - 1, video.scenes[i - 1].hold);
  }
  const e = ease(local / MOVE);
  const drift = 1 + DRIFT * Math.min(1, local / s.hold);
  return {
    scale: lerp(from.scale, target.scale, e) * drift,
    cx: lerp(from.cx, target.cx, e),
    cy: lerp(from.cy, target.cy, e),
  };
}

export function toScreen(cam, x, y) {
  return { x: W / 2 + (x - cam.cx) * cam.scale, y: ANCHOR_Y + (y - cam.cy) * cam.scale };
}

function layer(video, sizes, i, local) {
  const s = video.scenes[i];
  if (s.kind === "window") return { kind: "window", image: s.image, cam: camera(video, sizes, i, local) };
  if (s.kind === "phone") return { kind: "phone", images: s.images, lift: 40 * (1 - ease(local / 0.8)) };
  return { kind: "title", title: s.title, sub: s.sub || "", url: s.url || "", lift: 30 * (1 - ease(local / 0.8)) };
}

// Everything on screen at t.
export function frameAt(video, sizes, t) {
  const { i, local, scene: s } = locate(video, t);
  const prev = i > 0 ? video.scenes[i - 1] : null;
  const next = video.scenes[i + 1];
  const out = { index: i, layers: [], caption: null, ring: null, cursor: null };

  // A new picture fades in over the last frame of the one before; the same
  // picture just moves.
  const cur = layer(video, sizes, i, local);
  if (prev && !samePicture(prev, s) && local < FADE) {
    out.layers.push({ ...layer(video, sizes, i - 1, prev.hold), opacity: 1 });
    out.layers.push({ ...cur, opacity: ease(local / FADE) });
  } else {
    out.layers.push({ ...cur, opacity: i === 0 ? ease(local / FADE) : 1 });
  }
  const last = i === video.scenes.length - 1;
  if (last) {
    // The video ends on black, so a loop does not jump.
    const fadeOut = 1 - ramp(local, s.hold - 0.6, 0.6);
    for (const l of out.layers) l.opacity *= fadeOut;
  }

  if (s.caption) {
    // A caption carried on to the next scene stays up instead of blinking.
    const fromBefore = prev && prev.caption === s.caption;
    const toNext = next && next.caption === s.caption;
    const inE = fromBefore ? 1 : ease(ramp(local, 0.15, 0.4));
    const outE = toNext ? 1 : 1 - ease(ramp(local, s.hold - 0.3, 0.3));
    out.caption = { text: s.caption, sub: s.sub || "", opacity: inE * outE, rise: 24 * (1 - inE) };
  }

  if (s.kind === "window") {
    const cam = cur.cam;
    if (s.ring && s.focus) {
      const [x, y, w, h] = s.focus;
      const a = toScreen(cam, x, y);
      const b = toScreen(cam, x + w, y + h);
      const o = ease(ramp(local, MOVE, 0.3)) * (1 - ease(ramp(local, s.hold - 0.3, 0.3)));
      out.ring = { x: a.x, y: a.y, w: b.x - a.x, h: b.y - a.y, opacity: o };
    }
    if (s.click) {
      // The pointer comes from the last click on this picture, or from
      // below and to the right, then presses.
      let from = [s.click[0] + 160 / cam.scale, s.click[1] + 120 / cam.scale];
      if (prev && samePicture(prev, s) && prev.click) from = prev.click;
      const e = ease(ramp(local, 0.5, 0.9));
      const px = lerp(from[0], s.click[0], e);
      const py = lerp(from[1], s.click[1], e);
      const p = toScreen(cam, px, py);
      const press = ramp(local, 1.45, 0.5);
      out.cursor = {
        x: p.x, y: p.y,
        opacity: ease(ramp(local, 0.3, 0.3)) * (1 - ease(ramp(local, s.hold - 0.25, 0.25))),
        down: press > 0 && press < 0.35,
        ripple: press > 0 && press < 1 ? press : 0,
      };
    }
  }
  return out;
}

// How many frames a video is at fps, and the time of each.
export function frameTimes(video, fps) {
  const { total } = starts(video);
  const n = Math.round(total * fps);
  return Array.from({ length: n }, (_, k) => k / fps);
}
