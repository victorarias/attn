// Records one window to an H.264 mp4 until SIGINT/SIGTERM, then finalizes and
// exits 0. Usage: WindowRecorder <windowId> <outputPath> [fps]
//
// ScreenCaptureKit's desktop-independent window filter composites the window's
// own content wherever the window is — parked almost fully off-screen (the
// harness parks bridge-driven windows with ~20px visible), occluded, or moved.
// `screencapture -v -l` cannot do this: it records the screen region, so a
// parked window yields black frames (measured 2026-08-11; the SCK probe of a
// 97%-off-screen window returned its full content).
//
// SCK delivers frames only when the window repaints, so a quiet stretch adds
// no frames; the final re-append at stop time gives the file its true
// wall-clock duration, with still gaps in between.
//
// The window closing stops the stream (didStopWithError): the file is
// finalized with everything up to the close and the process exits 0 — the
// caller treats a self-exited recorder like a stopped one.

import AVFoundation
import CoreGraphics
import Foundation
import ScreenCaptureKit

func fail(_ message: String, code: Int32) -> Never {
    FileHandle.standardError.write("\(message)\n".data(using: .utf8)!)
    exit(code)
}

guard CommandLine.arguments.count >= 3, let windowIdArg = UInt32(CommandLine.arguments[1]) else {
    fail("usage: WindowRecorder <windowId> <outputPath> [fps]", code: 2)
}
let targetWindowId = CGWindowID(windowIdArg)
let outputURL = URL(fileURLWithPath: CommandLine.arguments[2])
let fps = CommandLine.arguments.count > 3 ? Int32(CommandLine.arguments[3]) ?? 15 : 15

// SkyLight asserts (CGS_REQUIRE_INIT) if ScreenCaptureKit touches the window
// server from a background thread before the process has a connection; force
// one on the main thread first.
_ = CGMainDisplayID()

final class Recorder: NSObject, SCStreamOutput, SCStreamDelegate {
    private var stream: SCStream?
    private var writer: AVAssetWriter?
    private var input: AVAssetWriterInput?
    private var adaptor: AVAssetWriterInputPixelBufferAdaptor?
    private var sessionStarted = false
    private var lastPixelBuffer: CVPixelBuffer?
    private var lastPTS = CMTime.zero
    private var finalizing = false
    private let queue = DispatchQueue(label: "window-recorder")

    func start() async throws {
        let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false)
        guard let window = content.windows.first(where: { $0.windowID == targetWindowId }) else {
            fail("window \(targetWindowId) not found among \(content.windows.count) shareable windows", code: 3)
        }
        let filter = SCContentFilter(desktopIndependentWindow: window)
        let scale = CGFloat(filter.pointPixelScale)
        // H.264 wants even dimensions.
        let width = max(2, Int(filter.contentRect.width * scale) & ~1)
        let height = max(2, Int(filter.contentRect.height * scale) & ~1)

        let writer = try AVAssetWriter(outputURL: outputURL, fileType: .mp4)
        let input = AVAssetWriterInput(mediaType: .video, outputSettings: [
            AVVideoCodecKey: AVVideoCodecType.h264,
            AVVideoWidthKey: width,
            AVVideoHeightKey: height,
        ])
        input.expectsMediaDataInRealTime = true
        let adaptor = AVAssetWriterInputPixelBufferAdaptor(assetWriterInput: input, sourcePixelBufferAttributes: nil)
        guard writer.canAdd(input) else { fail("writer rejected video input", code: 4) }
        writer.add(input)
        guard writer.startWriting() else {
            fail("writer failed to start: \(writer.error?.localizedDescription ?? "unknown")", code: 4)
        }
        self.writer = writer
        self.input = input
        self.adaptor = adaptor

        let config = SCStreamConfiguration()
        config.width = width
        config.height = height
        config.minimumFrameInterval = CMTime(value: 1, timescale: fps)
        config.queueDepth = 8
        config.showsCursor = true

        let stream = SCStream(filter: filter, configuration: config, delegate: self)
        try stream.addStreamOutput(self, type: .screen, sampleHandlerQueue: queue)
        try await stream.startCapture()
        self.stream = stream
    }

    func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer, of type: SCStreamOutputType) {
        guard type == .screen, !finalizing,
              let attachments = CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]],
              let statusRaw = attachments.first?[.status] as? Int,
              SCFrameStatus(rawValue: statusRaw) == .complete,
              let pixelBuffer = CMSampleBufferGetImageBuffer(sampleBuffer),
              let input, let adaptor, input.isReadyForMoreMediaData
        else { return }
        let pts = CMSampleBufferGetPresentationTimeStamp(sampleBuffer)
        if !sessionStarted {
            writer?.startSession(atSourceTime: pts)
            sessionStarted = true
        }
        adaptor.append(pixelBuffer, withPresentationTime: pts)
        lastPixelBuffer = pixelBuffer
        lastPTS = pts
    }

    func stream(_ stream: SCStream, didStopWithError error: Error) {
        FileHandle.standardError.write("stream stopped: \(error.localizedDescription)\n".data(using: .utf8)!)
        stopAndFinalize()
    }

    func stopAndFinalize() {
        queue.async { [self] in
            if finalizing { return }
            finalizing = true
            guard let writer, let input, sessionStarted else {
                // No frame ever arrived; a zero-frame mp4 is useless, so leave
                // nothing behind and let the caller report the empty segment.
                try? FileManager.default.removeItem(at: outputURL)
                exit(0)
            }
            // Re-append the last frame stamped now, so the file spans the real
            // recording window even when the app repainted rarely.
            let now = CMClockGetTime(CMClockGetHostTimeClock())
            if let lastPixelBuffer, let adaptor, input.isReadyForMoreMediaData, now > lastPTS {
                adaptor.append(lastPixelBuffer, withPresentationTime: now)
            }
            input.markAsFinished()
            writer.finishWriting {
                exit(writer.status == .completed ? 0 : 4)
            }
        }
    }
}

let recorder = Recorder()

let signalQueue = DispatchQueue(label: "window-recorder-signals")
var signalSources: [DispatchSourceSignal] = []
for sig in [SIGINT, SIGTERM] {
    signal(sig, SIG_IGN)
    let source = DispatchSource.makeSignalSource(signal: sig, queue: signalQueue)
    source.setEventHandler { recorder.stopAndFinalize() }
    source.resume()
    signalSources.append(source)
}

Task {
    do {
        try await recorder.start()
    } catch {
        fail("capture failed to start: \(error.localizedDescription)", code: 4)
    }
}

dispatchMain()
