import {
  configurePanelFocus,
  syncPanelFocusFromTarget,
  resolvePanelFocus,
} from "./panel-focus.js";
import * as topbar from "./topbar.js";
import * as feed from "./feed-panel.js";
import * as content from "./content-panel.js";
import * as mobile from "./mobile.js";
import { bindMobileReaderNavigation } from "./mobile-navigation.js";
import { bindKeyboardShortcuts } from "./keyboard.js";
import { bindHTMXLifecycle } from "./htmx.js";

configurePanelFocus({
  isFeedEditMode: feed.isFeedEditMode,
  isContentPanelOpen: content.isContentPanelOpen,
});

content.bindContentPanelInteractions();
feed.bindFeedPanelInteractions();

if (document.body.dataset.panelFocusTrackingBound !== "true") {
  document.body.dataset.panelFocusTrackingBound = "true";
  document.addEventListener("focusin", (event) => {
    syncPanelFocusFromTarget(event.target);
  });
  document.addEventListener("click", (event) => {
    syncPanelFocusFromTarget(event.target);
  });
}

bindKeyboardShortcuts({
  topbar,
  feed,
  content,
  resolvePanelFocus,
});

bindHTMXLifecycle({
  topbar,
  feed,
  content,
});

bindMobileReaderNavigation();
mobile.bindMobileBootstrap();
