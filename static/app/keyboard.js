import { expandedPanelScrollStep } from "./state.js";
import { isDesktopLayout } from "./dom.js";

export const bindKeyboardShortcuts = ({
  topbar,
  feed,
  content,
  resolvePanelFocus,
}) => {
  if (document.body.dataset.keyboardShortcutsBound === "true") {
    return;
  }
  document.body.dataset.keyboardShortcutsBound = "true";

  const isFeedEditMode = () => feed.isFeedEditMode();

  const moveWithinFocusedPanel = (delta, panel) => {
    if (panel === "feed") {
      return feed.moveSelectedFeed(delta);
    }
    if (panel === "content") {
      return content.scrollExpandedPanel(delta * expandedPanelScrollStep);
    }
    content.moveActive(delta);
    return true;
  };

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && topbar.isTopbarShortcutsOpen()) {
      topbar.setTopbarShortcutsOpen(false);
      return;
    }
    if (
      event.key === "Escape" &&
      content.isContentPanelOpen() &&
      content.isContentPanelFloating()
    ) {
      event.preventDefault();
      content.closeContentPanel();
      return;
    }
    if (feed.handleFeedEditModeKeydown(event)) {
      return;
    }
    if (content.shouldIgnore(event)) {
      return;
    }
    if (!getItemList() && !feed.getFeedLinks({ visibleOnly: true }).length) {
      return;
    }

    const key = event.key.toLowerCase();
    const desktopPanelNavigationEnabled = isDesktopLayout() && !isFeedEditMode();
    const panel = desktopPanelNavigationEnabled
      ? resolvePanelFocus(event.target || document.activeElement)
      : "items";
    const prevent = () => {
      event.preventDefault();
    };

    switch (key) {
      case "j":
      case "arrowdown":
        prevent();
        if (desktopPanelNavigationEnabled) {
          moveWithinFocusedPanel(1, panel);
          break;
        }
        if (key === "arrowdown" && content.scrollExpandedPanel(expandedPanelScrollStep)) {
          break;
        }
        content.moveActive(1);
        break;
      case "k":
      case "arrowup":
        prevent();
        if (desktopPanelNavigationEnabled) {
          moveWithinFocusedPanel(-1, panel);
          break;
        }
        if (key === "arrowup" && content.scrollExpandedPanel(-expandedPanelScrollStep)) {
          break;
        }
        content.moveActive(-1);
        break;
      case "l":
      case "arrowright":
        prevent();
        if (desktopPanelNavigationEnabled) {
          if (panel === "feed") {
            feed.openSelectedFeed();
            break;
          }
          if (panel === "items") {
            content.expandActiveToContentPanel();
            break;
          }
          content.focusContentPanel();
          break;
        }
        content.toggleExpanded(true);
        break;
      case "h":
      case "arrowleft":
        prevent();
        if (desktopPanelNavigationEnabled) {
          if (panel === "content") {
            content.collapseContentPanelToItems();
            break;
          }
          feed.focusFeedPanel();
          break;
        }
        content.toggleExpanded(false);
        break;
      case "o":
        prevent();
        content.openActiveLink();
        break;
      case "enter": {
        if (
          desktopPanelNavigationEnabled &&
          panel === "content" &&
          content.isContentPanelOpen() &&
          !content.isContentPanelFloating()
        ) {
          prevent();
          content.setContentPanelFloating(true);
          break;
        }
        const main = document.getElementById("main-content");
        if (
          main &&
          event.target &&
          event.target !== document.body &&
          !main.contains(event.target)
        ) {
          break;
        }
        prevent();
        content.openActiveLink();
        break;
      }
      case "r":
        prevent();
        content.toggleRead();
        break;
      default:
        break;
    }
  });
};

const getItemList = () => document.getElementById("item-list");
