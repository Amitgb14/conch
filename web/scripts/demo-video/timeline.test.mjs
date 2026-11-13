// Run by TestDemoVideoTimeline: node timeline.test.mjs
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  ANCHOR_Y, FADE, MAX_ZOOM, MOVE, W, ease, frame, frameAt, frameTimes, locate, starts, toScreen,
} from "./timeline.mjs";

const near = (a, b, msg) => assert.ok(Math.abs(a - b) < 1e-6, `${msg}: ${a} != ${b}`);
let failed = 0;
function test(name, fn) {
  try {
    fn();
  } catch (e) {
    failed++;
    console.error(`FAIL ${name}\n  ${e.message}`);
  }
}

const IMG = "a.png";
const sizes = { [IMG]: [1320, 820], "b.png": [1320, 820] };
const video = {
  scenes: [
    { kind: "title", title: "t", hold: 2 },
    { kind: "window", image: IMG, caption: "whole", hold: 3 },
    { kind: "window", image: IMG, focus: [0, 0, 200, 100], ring: true, click: [50, 50], caption: "zoom", hold: 3 },
    { kind: "window", image: IMG, focus: [1000, 600, 300, 200], click: [1100, 700], caption: "zoom", hold: 3 },
    { kind: "window", image: "b.png", caption: "other", hold: 2 },
    { kind: "title", title: "end", hold: 2 },
  ],
};

test("ease ends where it should and stays in range", () => {
  near(ease(0), 0, "0");
  near(ease(1), 1, "1");
  near(ease(0.5), 0.5, "half");
  assert.equal(ease(-3), 0);
  assert.equal(ease(9), 1);
});

test("starts adds the holds", () => {
  const { at, total } = starts(video);
  assert.deepEqual(at, [0, 2, 5, 8, 11, 13]);
  assert.equal(total, 15);
});

test("locate finds the scene, at the edges too", () => {
  assert.equal(locate(video, 0).i, 0);
  assert.equal(locate(video, 1.999).i, 0);
  assert.equal(locate(video, 2).i, 1);
  near(locate(video, 2.5).local, 0.5, "local");
  assert.equal(locate(video, -1).i, 0, "before the start is the first scene");
  assert.equal(locate(video, 99).i, 5, "past the end is the last scene");
});

test("frame fits the whole picture without zooming past the cap", () => {
  const whole = frame(null, [1320, 820]);
  assert.ok(whole.scale * 1320 <= 0.88 * W + 1e-6);
  near(whole.cx, 660, "cx");
  const tiny = frame([10, 10, 50, 20], [1320, 820]);
  assert.equal(tiny.scale, MAX_ZOOM, "a small focus is capped");
  // A picture larger than the frame is shrunk, and a focus never zooms out
  // past the whole picture.
  const big = frame(null, [4000, 2000]);
  assert.ok(big.scale < 1);
  assert.ok(frame([0, 0, 4000, 2000], [4000, 2000]).scale >= big.scale - 1e-9);
});

test("the camera moves continuously from one focus to the next", () => {
  const step = 1 / 120;
  let last = null;
  for (let t = 2; t < 11; t += step) {
    const f = frameAt(video, sizes, t);
    const cam = f.layers[f.layers.length - 1].cam;
    if (last) {
      assert.ok(Math.abs(cam.scale - last.scale) < 0.05, `scale jumps at ${t}`);
      assert.ok(Math.abs(cam.cx - last.cx) < 30 && Math.abs(cam.cy - last.cy) < 30, `pans jump at ${t}`);
    }
    last = cam;
  }
});

test("after the move the focus is centred", () => {
  const f = frameAt(video, sizes, 5 + MOVE + 0.01);
  const cam = f.layers[0].cam;
  const c = toScreen(cam, 100, 50);
  near(c.x, W / 2, "x");
  near(c.y, ANCHOR_Y, "y");
});

test("a new picture fades in over the old one", () => {
  const f = frameAt(video, sizes, 11 + FADE / 2);
  assert.equal(f.layers.length, 2);
  assert.equal(f.layers[0].image, IMG);
  assert.equal(f.layers[1].image, "b.png");
  assert.ok(f.layers[1].opacity > 0 && f.layers[1].opacity < 1);
  assert.equal(frameAt(video, sizes, 11 + FADE + 0.01).layers.length, 1);
});

test("a caption carried to the next scene does not blink", () => {
  for (let t = 5.6; t < 10.5; t += 0.05) {
    const c = frameAt(video, sizes, t).caption;
    assert.equal(c.text, "zoom");
    near(c.opacity, 1, `opacity at ${t}`);
  }
  // ...while one that changes fades out and in.
  assert.ok(frameAt(video, sizes, 4.95).caption.opacity < 0.5);
  assert.ok(frameAt(video, sizes, 2.05).caption.opacity < 0.5);
});

test("title scenes have no caption, ring or cursor", () => {
  const f = frameAt(video, sizes, 1);
  assert.equal(f.caption, null);
  assert.equal(f.ring, null);
  assert.equal(f.cursor, null);
  assert.equal(f.layers[0].kind, "title");
});

test("the ring shows once the camera settles, around the focus", () => {
  assert.equal(frameAt(video, sizes, 5.2).ring.opacity, 0);
  const f = frameAt(video, sizes, 6.5);
  near(f.ring.opacity, 1, "opacity");
  near(f.ring.x + f.ring.w / 2, W / 2, "centred");
});

test("the cursor arrives at the click and presses", () => {
  const f = frameAt(video, sizes, 5 + 1.6);
  const cam = f.layers[0].cam;
  const at = toScreen(cam, 50, 50);
  near(f.cursor.x, at.x, "x");
  near(f.cursor.y, at.y, "y");
  assert.ok(f.cursor.ripple > 0);
  // The next scene's pointer starts from this click.
  const g = frameAt(video, sizes, 8 + 0.4);
  const from = toScreen(g.layers[0].cam, 50, 50);
  near(g.cursor.x, from.x, "starts where the last click was");
});

test("the video starts and ends on black", () => {
  near(frameAt(video, sizes, 0).layers[0].opacity, 0, "start");
  assert.ok(frameAt(video, sizes, 14.99).layers[0].opacity < 0.05, "end");
});

test("frameTimes counts frames at the rate", () => {
  const ts = frameTimes(video, 30);
  assert.equal(ts.length, 450);
  assert.equal(ts[0], 0);
  assert.ok(ts[ts.length - 1] < 15);
});

test("every video in the storyboard draws every frame", () => {
  const sb = JSON.parse(readFileSync(new URL("./videos.json", import.meta.url)));
  // Sizes do not matter to whether a frame can be worked out.
  const any = new Proxy({}, { get: () => [1320, 820] });
  for (const v of sb.videos) {
    for (const t of frameTimes(v, 10)) {
      const f = frameAt(v, any, t);
      for (const l of f.layers) {
        assert.ok(l.opacity >= 0 && l.opacity <= 1, `${v.name} at ${t}: opacity ${l.opacity}`);
        if (l.cam) assert.ok(Number.isFinite(l.cam.scale) && l.cam.scale > 0, `${v.name} at ${t}: scale`);
      }
    }
  }
});

if (failed) {
  console.error(`${failed} failed`);
  process.exit(1);
}
console.log("ok");
