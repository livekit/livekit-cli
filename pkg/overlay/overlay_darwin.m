// Copyright 2026 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <pthread.h>

#include "overlay_darwin.h"
#include "_cgo_export.h"

// Built without ARC. The window, web view, and handler live for the rest of
// the process once created, so they're never released.

// Borderless windows can't become key by default, which would leave the page
// without keyboard input (Esc to close).
@interface LKOverlayWindow : NSWindow
@end

@implementation LKOverlayWindow
- (BOOL)canBecomeKeyWindow {
	return YES;
}
- (BOOL)canBecomeMainWindow {
	return YES;
}
// Closing the window, such as with its close button in windowed mode,
// closes the overlay the same as when the page asks to. Closing only the
// window would leave Run blocked.
- (void)close {
	lkOverlayMessage((char *)"{\"type\":\"close\"}");
}
@end

@interface LKOverlayHandler : NSObject <WKScriptMessageHandler>
@end

@implementation LKOverlayHandler
- (void)userContentController:(WKUserContentController *)controller
      didReceiveScriptMessage:(WKScriptMessage *)message {
	if (![message.body isKindOfClass:[NSString class]]) {
		return;
	}
	lkOverlayMessage((char *)[(NSString *)message.body UTF8String]);
}
@end

static LKOverlayWindow *gWindow;
static WKWebView *gWebView;
static NSVisualEffectView *gBlur;
static BOOL gClosing;

void lk_overlay_run(const char *html, int windowed, int inspectable) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		// No Dock icon or menu bar takeover: the overlay floats over whatever
		// app the user is in.
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];

		NSScreen *screen = [NSScreen mainScreen];
		NSRect frame = screen.frame;
		NSWindowStyleMask style = NSWindowStyleMaskBorderless;
		if (windowed) {
			NSRect visible = screen.visibleFrame;
			frame = NSInsetRect(visible, visible.size.width * 0.12, visible.size.height * 0.12);
			style = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
			        NSWindowStyleMaskResizable | NSWindowStyleMaskFullSizeContentView;
		}

		gWindow = [[LKOverlayWindow alloc] initWithContentRect:frame
		                                             styleMask:style
		                                               backing:NSBackingStoreBuffered
		                                                 defer:NO];
		gWindow.opaque = NO;
		gWindow.backgroundColor = [NSColor clearColor];
		gWindow.releasedWhenClosed = NO;
		gWindow.appearance = [NSAppearance appearanceNamed:NSAppearanceNameDarkAqua];
		gWindow.alphaValue = 0;
		if (windowed) {
			gWindow.titlebarAppearsTransparent = YES;
			gWindow.titleVisibility = NSWindowTitleHidden;
			gWindow.level = NSFloatingWindowLevel;
		} else {
			gWindow.hasShadow = NO;
			// Above the menu bar and Dock.
			gWindow.level = NSStatusWindowLevel;
			gWindow.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces |
			                             NSWindowCollectionBehaviorFullScreenAuxiliary |
			                             NSWindowCollectionBehaviorStationary |
			                             NSWindowCollectionBehaviorIgnoresCycle;
		}

		// The blur and the web view are siblings, so peek can fade the blur
		// without fading the page.
		NSView *root = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, frame.size.width, frame.size.height)];
		root.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
		gWindow.contentView = root;

		// Blurs whatever is behind the window: the desktop and other apps.
		NSVisualEffectView *blur = [[NSVisualEffectView alloc] initWithFrame:root.bounds];
		blur.blendingMode = NSVisualEffectBlendingModeBehindWindow;
		blur.material = NSVisualEffectMaterialFullScreenUI;
		blur.state = NSVisualEffectStateActive;
		blur.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
		[root addSubview:blur];
		gBlur = blur;

		WKWebViewConfiguration *config = [[WKWebViewConfiguration alloc] init];
		// Earcons play from Web Audio as soon as the overlay opens, without a click first.
		config.mediaTypesRequiringUserActionForPlayback = WKAudiovisualMediaTypeNone;
		[config.userContentController addScriptMessageHandler:[[LKOverlayHandler alloc] init] name:@"lk"];

		gWebView = [[WKWebView alloc] initWithFrame:root.bounds configuration:config];
		gWebView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
		// Transparent page background so the blur shows through.
		[gWebView setValue:@NO forKey:@"drawsBackground"];
		if (inspectable) {
			if (@available(macOS 13.3, *)) {
				gWebView.inspectable = YES;
			}
		}
		[root addSubview:gWebView];
		[gWebView loadHTMLString:[NSString stringWithUTF8String:html] baseURL:nil];

		[gWindow makeKeyAndOrderFront:nil];
		[gWindow makeFirstResponder:gWebView];
		[NSApp activateIgnoringOtherApps:YES];
		[NSAnimationContext runAnimationGroup:^(NSAnimationContext *ctx) {
			ctx.duration = 0.35;
			gWindow.animator.alphaValue = 1;
		}];

		[NSApp run];

		[gWindow orderOut:nil];
	}
}

// Messages go through one fixed function body with the JSON as an argument.
// Evaluating each message as its own script source makes WebKit compile and
// cache a new script per message, many times a second.
static NSString *const kReceiveBody = @"if (window.lk) window.lk.receive(JSON.parse(msg));";

void lk_overlay_send(const char *json) {
	@autoreleasepool {
		NSString *msg = [NSString stringWithUTF8String:json];
		dispatch_async(dispatch_get_main_queue(), ^{
			[gWebView callAsyncJavaScript:kReceiveBody
			                    arguments:@{@"msg" : msg}
			                      inFrame:nil
			               inContentWorld:WKContentWorld.pageWorld
			            completionHandler:nil];
		});
	}
}

static void stopApp(void) {
	[NSApp stop:nil];
	// -stop: takes effect after the next event, so post one.
	NSEvent *event = [NSEvent otherEventWithType:NSEventTypeApplicationDefined
	                                    location:NSZeroPoint
	                               modifierFlags:0
	                                   timestamp:0
	                                windowNumber:0
	                                     context:nil
	                                     subtype:0
	                                       data1:0
	                                       data2:0];
	[NSApp postEvent:event atStart:YES];
}

void lk_overlay_close(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (gWindow == nil || gClosing) {
			return;
		}
		gClosing = YES;
		[NSAnimationContext
		    runAnimationGroup:^(NSAnimationContext *ctx) {
			    ctx.duration = 0.25;
			    gWindow.animator.alphaValue = 0;
		    }
		    completionHandler:^{
			    stopApp();
		    }];
	});
}

void lk_overlay_set_peek(int on) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSAnimationContext runAnimationGroup:^(NSAnimationContext *ctx) {
			ctx.duration = 0.2;
			gBlur.animator.alphaValue = on ? 0 : 1;
		}];
	});
}

int lk_overlay_is_main_thread(void) {
	return pthread_main_np();
}
