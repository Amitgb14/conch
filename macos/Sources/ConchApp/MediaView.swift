import AVKit
import AppKit
import ConchKit
import PDFKit
import SwiftUI
import WebKit

// A file shown as what it is rather than as text: a picture to zoom, a
// video or sound to play, a PDF to page through, a page rendered. Each is
// AppKit's own view for the thing, so zoom, scrubbing, Picture in
// Picture, search in a PDF and the rest come as macOS has them.

struct MediaView: View {
    let kind: MediaKind
    let url: URL
    /// The folder a page may read beside it (its CSS, its pictures).
    var readAccess: URL?

    var body: some View {
        content
            // A player or a page asks for its own size; the pane decides.
            .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
            .clipped()
    }

    @ViewBuilder var content: some View {
        switch kind {
        case .image where url.pathExtension.lowercased() == "svg":
            // NSImage's SVG is partial before macOS 14; WebKit draws all of it.
            PageView(url: url, readAccess: readAccess ?? url.deletingLastPathComponent(), background: true)
        case .image: ImageView(url: url)
        case .video, .audio: PlayerView(url: url)
        case .pdf: PDFFileView(url: url)
        case .html: PageView(url: url, readAccess: readAccess ?? url.deletingLastPathComponent())
        case .text: EmptyView()
        }
    }
}

/// A picture, fitted to begin with: pinch or ⌘-scroll to zoom, double-click
/// to go back to fitting.
struct ImageView: NSViewRepresentable {
    let url: URL

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = ZoomScroll()
        scroll.allowsMagnification = true
        scroll.minMagnification = 0.05
        scroll.maxMagnification = 32
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = true
        scroll.autohidesScrollers = true
        scroll.drawsBackground = false
        scroll.documentView = NSImageView()
        load(scroll)
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        if (scroll as? ZoomScroll)?.shown != url { load(scroll) }
    }

    private func load(_ scroll: NSScrollView) {
        guard let iv = scroll.documentView as? NSImageView else { return }
        (scroll as? ZoomScroll)?.shown = url
        let img = NSImage(contentsOf: url)
        iv.image = img
        iv.imageScaling = .scaleProportionallyUpOrDown
        iv.animates = true // a GIF moves
        let size = img?.size ?? .zero
        iv.frame = NSRect(origin: .zero, size: size)
        DispatchQueue.main.async { (scroll as? ZoomScroll)?.fit() }
    }

    final class ZoomScroll: NSScrollView {
        var shown: URL?
        /// The whole picture in view, never blown up past its own size.
        func fit() {
            guard let doc = documentView, doc.frame.width > 0, doc.frame.height > 0, bounds.width > 0 else { return }
            let m = min(1, min(bounds.width / doc.frame.width, bounds.height / doc.frame.height))
            magnification = max(minMagnification, m)
            centre()
        }
        func centre() {
            guard let doc = documentView else { return }
            let visible = contentView.bounds.size
            contentView.scroll(to: NSPoint(x: (doc.frame.width - visible.width) / 2, y: (doc.frame.height - visible.height) / 2))
        }
        override func mouseDown(with event: NSEvent) {
            if event.clickCount == 2 { fit() } else { super.mouseDown(with: event) }
        }
        override func scrollWheel(with event: NSEvent) {
            guard event.modifierFlags.contains(.command) else { return super.scrollWheel(with: event) }
            let at = contentView.convert(event.locationInWindow, from: nil)
            setMagnification(magnification * (1 + event.scrollingDeltaY / 100), centeredAt: at)
        }
    }
}

/// A video or a sound, with the system's controls.
struct PlayerView: NSViewRepresentable {
    let url: URL

    func makeNSView(context: Context) -> AVPlayerView {
        let v = AVPlayerView()
        v.controlsStyle = .floating
        v.showsFullScreenToggleButton = true
        v.allowsPictureInPicturePlayback = true
        v.player = AVPlayer(url: url)
        return v
    }

    func updateNSView(_ v: AVPlayerView, context: Context) {
        if (v.player?.currentItem?.asset as? AVURLAsset)?.url != url { v.player = AVPlayer(url: url) }
    }

    static func dismantleNSView(_ v: AVPlayerView, coordinator: ()) {
        v.player?.pause() // a closed tab goes quiet
    }
}

struct PDFFileView: NSViewRepresentable {
    let url: URL

    func makeNSView(context: Context) -> PDFView {
        let v = PDFView()
        v.autoScales = true
        v.displayMode = .singlePageContinuous
        v.document = PDFDocument(url: url)
        return v
    }

    func updateNSView(_ v: PDFView, context: Context) {
        if v.document?.documentURL != url { v.document = PDFDocument(url: url) }
    }
}

/// A file rendered by WebKit: a page, or an SVG.
struct PageView: NSViewRepresentable {
    let url: URL
    let readAccess: URL
    /// A picture on its own wants no white page behind it.
    var background = false

    func makeNSView(context: Context) -> WKWebView {
        let v = WKWebView(frame: .zero, configuration: WKWebViewConfiguration())
        if background { v.setValue(false, forKey: "drawsBackground") }
        v.allowsMagnification = true
        v.loadFileURL(url, allowingReadAccessTo: readAccess)
        return v
    }

    func updateNSView(_ v: WKWebView, context: Context) {
        if v.url?.standardizedFileURL != url.standardizedFileURL { v.loadFileURL(url, allowingReadAccessTo: readAccess) }
    }
}

/// A remote file copied whole to dest through fs.download, a chunk at a
/// time; progress may say stop (another file was picked).
@MainActor
func fetchFile(_ c: Connection, _ path: String, to dest: URL, progress: @escaping (Int64, Int64) -> Bool) async throws {
    struct Params: Encodable { var path: String }
    struct Download: Decodable { var id: String; var size: Int64?; var dir: Bool? }
    struct ReadParams: Encodable { var id: String; var offset: Int64 }
    struct Chunk: Decodable { var data: Data?; var eof: Bool? }
    let d = try await c.call("fs.download", Params(path: path), as: Download.self)
    if d.dir == true { throw CocoaError(.fileReadUnknown) }
    let fm = FileManager.default
    try fm.createDirectory(at: dest.deletingLastPathComponent(), withIntermediateDirectories: true)
    let part = dest.appendingPathExtension("part")
    fm.createFile(atPath: part.path, contents: nil)
    let h = try FileHandle(forWritingTo: part)
    var done: Int64 = 0
    do {
        while true {
            let ch = try await c.call("fs.download_read", ReadParams(id: d.id, offset: done), as: Chunk.self)
            if let data = ch.data, !data.isEmpty {
                try h.write(contentsOf: data)
                done += Int64(data.count)
            }
            guard progress(done, d.size ?? 0) else { throw CancellationError() }
            if ch.eof == true || (ch.data ?? Data()).isEmpty { break }
            if done > mediaFetchMax { throw CocoaError(.fileReadTooLarge) }
        }
        try h.close()
        _ = try? fm.removeItem(at: dest)
        try fm.moveItem(at: part, to: dest)
    } catch {
        try? h.close()
        try? fm.removeItem(at: part)
        throw error
    }
}

/// Where fetched files are kept: the app's cache, which macOS may clear.
@MainActor func mediaCache(machine: String, path: String) -> URL {
    let base = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first
        ?? URL(fileURLWithPath: NSTemporaryDirectory())
    return mediaCachePath(base: base.appendingPathComponent("dev.conch.mac", isDirectory: true), machine: machine, path: path)
}
