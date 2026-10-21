package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>

// Wails v2 quits with [NSApp stop:] from a goroutine and never posts an
// event. stop: only ends [NSApp run] once the next event is processed, so
// the run loop stays blocked in mach_msg and the process keeps running
// (Dock icon stays) until it is force-quit. Stop on the main thread and
// post an event so the loop actually returns.
static void stopMacRunLoopOnMain(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSApp abortModal];
		[NSApp stop:nil];
		NSEvent *event = [NSEvent otherEventWithType:NSEventTypeApplicationDefined
		                                    location:NSMakePoint(0, 0)
		                               modifierFlags:0
		                                   timestamp:0
		                                windowNumber:0
		                                     context:nil
		                                     subtype:0
		                                       data1:0
		                                       data2:0];
		[NSApp postEvent:event atStart:YES];
	});
}
*/
import "C"

// stopMacRunLoop schedules the real macOS quit. It reports that it did, so
// beforeClose can keep Wails from calling [NSApp stop:] off the main thread.
func stopMacRunLoop() bool {
	C.stopMacRunLoopOnMain()
	return true
}
