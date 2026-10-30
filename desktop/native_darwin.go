package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#include <stdlib.h>

// The Dock's badge and a system sound belong to the main thread.
static void conchSetBadge(const char *label) {
	NSString *s = [NSString stringWithUTF8String:label];
	dispatch_async(dispatch_get_main_queue(), ^{
		[[NSApp dockTile] setBadgeLabel:(s.length ? s : nil)];
	});
}

static void conchPlaySound(const char *name) {
	NSString *s = [NSString stringWithUTF8String:name];
	dispatch_async(dispatch_get_main_queue(), ^{
		[[NSSound soundNamed:s] play];
	});
}
*/
import "C"

import "unsafe"

func setBadge(label string) {
	cs := C.CString(label)
	defer C.free(unsafe.Pointer(cs))
	C.conchSetBadge(cs)
}

func playSound(name string) {
	cs := C.CString(name)
	defer C.free(unsafe.Pointer(cs))
	C.conchPlaySound(cs)
}
