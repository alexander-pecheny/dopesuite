package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// centreTrafficLights stretches the title bar to height and centres the window
// buttons in it, keeping their x. AppKit lays the title bar out again on every
// resize, so this runs after each one.
static void centreTrafficLights(void *window, double height) {
	NSWindow *w = (NSWindow *)window;
	dispatch_async(dispatch_get_main_queue(), ^{
		NSButton *close = [w standardWindowButton:NSWindowCloseButton];
		if (close == nil) return;
		NSView *bar = close.superview.superview;
		NSRect r = bar.frame;
		r.size.height = height;
		r.origin.y = NSHeight(w.frame) - height;
		bar.frame = r;
		for (NSWindowButton kind = NSWindowCloseButton; kind <= NSWindowZoomButton; kind++) {
			NSButton *b = [w standardWindowButton:kind];
			NSRect f = b.frame;
			f.origin.y = (height - NSHeight(f)) / 2;
			b.frame = f;
		}
	});
}
*/
import "C"

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// stripHeight is the tab strip's height in shell/index.html.
const stripHeight = 40

func centreTrafficLights(w *application.WebviewWindow) {
	centre := func(*application.WindowEvent) {
		if p := w.NativeWindow(); p != nil {
			C.centreTrafficLights(p, stripHeight)
		}
	}
	for _, e := range []events.WindowEventType{
		events.Mac.WindowShow, events.Mac.WindowDidResize,
		events.Mac.WindowDidBecomeKey, events.Mac.WindowDidExitFullScreen,
	} {
		w.OnWindowEvent(e, centre)
	}
}
