//go:build smoke

//nolint:testpackage // Smoke tests intentionally exercise unexported test helpers and wiring.
package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

func hasClassExpression(selector, className string) string {
	return fmt.Sprintf(
		`(() => { const el = document.querySelector(%q); return !!el && el.classList.contains(%q); })()`,
		selector,
		className,
	)
}

func inactiveReaderBoundaryExpression() string {
	return `(() => {
		const root = document.querySelector('[data-reader-content="true"]');
		if (!root || !root.hasAttribute("hx-disable")) {
			return false;
		}
		const activeSelector = [
			"[hx-get]", "[hx-post]", "[hx-put]", "[hx-delete]", "[hx-patch]", "[hx-trigger]",
			"[data-hx-get]", "[data-hx-post]", "[data-hx-put]", "[data-hx-delete]", "[data-hx-patch]",
			"[data-hx-trigger]", "form", "iframe", "script", "style", "svg", "math",
		].join(",");
		return root.textContent.includes("Inactive boundary smoke content") && !root.querySelector(activeSelector);
	})()`
}

func pathnameExpression(path string) string {
	return fmt.Sprintf(`(() => window.location.pathname === %q)()`, path)
}

func requestURIExpression(path string) string {
	return fmt.Sprintf(`(() => (window.location.pathname + window.location.search) === %q)()`, path)
}

func mobileFilterValueExpression(feedID int64) string {
	return fmt.Sprintf(
		`(() => {
			const select = document.querySelector("#mobile-stream-feed-filter");
			return !!select && select.value === %q;
		})()`,
		fmt.Sprintf("%d", feedID),
	)
}

func elementPresentExpression(selector string) string {
	return fmt.Sprintf(
		`(() => !!document.querySelector(%q))()`,
		selector,
	)
}

func elementAbsentExpression(selector string) string {
	return fmt.Sprintf(
		`(() => !document.querySelector(%q))()`,
		selector,
	)
}

func elementHiddenExpression(selector string) string {
	return fmt.Sprintf(
		`(() => {
			const el = document.querySelector(%q);
			return !!el && el.getClientRects().length === 0;
		})()`,
		selector,
	)
}

func elementVisibleExpression(selector string) string {
	return fmt.Sprintf(
		`(() => {
			const el = document.querySelector(%q);
			return !!el && el.getClientRects().length > 0;
		})()`,
		selector,
	)
}

func generalMenuOpenExpression() string {
	return `(() => {
		const button = document.querySelector("#topbar-shortcuts-button");
		const panel = document.querySelector("#topbar-shortcuts-panel");
		return !!button && !!panel && button.getAttribute("aria-expanded") === "true" &&
			!panel.hidden && panel.getClientRects().length > 0;
	})()`
}

func generalMenuClosedExpression() string {
	return `(() => {
		const button = document.querySelector("#topbar-shortcuts-button");
		const panel = document.querySelector("#topbar-shortcuts-panel");
		return !!button && !!panel && button.getAttribute("aria-expanded") === "false" &&
			panel.hidden && panel.getClientRects().length === 0;
	})()`
}

func generalMenuCompactExpression() string {
	return `(() => {
		const panel = document.querySelector("#topbar-shortcuts-panel");
		if (!panel || panel.hidden) return false;
		const rect = panel.getBoundingClientRect();
		return getComputedStyle(panel).overflowY === "auto" && panel.scrollHeight > panel.clientHeight &&
			rect.top >= 0 && rect.bottom <= innerHeight + 1;
	})()`
}

func generalMenuArrowScrollExpression() string {
	return `(() => {
		const panel = document.querySelector("#topbar-shortcuts-panel");
		const active = document.querySelector("#item-list .item-entry.is-active");
		return !!panel && !!active && document.activeElement === panel && panel.scrollTop > 0 &&
			active.id === window.__menuReaderActiveID;
	})()`
}

func generalMenuEndVisibleExpression() string {
	return `(() => {
		const panel = document.querySelector("#topbar-shortcuts-panel");
		const lastRow = panel && panel.querySelector('[data-menu-section="shortcuts"] .topbar-shortcuts-row:last-child');
		if (!panel || !lastRow) return false;
		const panelRect = panel.getBoundingClientRect();
		const rowRect = lastRow.getBoundingClientRect();
		return panel.scrollTop > 0 && rowRect.top >= panelRect.top && rowRect.bottom <= panelRect.bottom + 1;
	})()`
}

func focusOutlineVisibleExpression(selector string) string {
	return fmt.Sprintf(
		`(() => {
			const el = document.querySelector(%q);
			if (!el || document.activeElement !== el) return false;
			const style = getComputedStyle(el);
			return style.outlineStyle !== "none" && parseFloat(style.outlineWidth) >= 2;
		})()`,
		selector,
	)
}

func itemActionHierarchyExpression(primarySelector, secondarySelector string) string {
	return fmt.Sprintf(
		`(() => {
			const primary = document.querySelector(%q);
			const secondary = document.querySelector(%q);
			if (!primary || !secondary) return false;
			const primaryStyle = getComputedStyle(primary);
			const secondaryStyle = getComputedStyle(secondary);
			return parseInt(primaryStyle.fontWeight, 10) >= 600 &&
				parseFloat(primaryStyle.fontSize) > parseFloat(secondaryStyle.fontSize);
		})()`,
		primarySelector,
		secondarySelector,
	)
}

func itemReadToggleStateExpression(selector, state, action string, checked bool) string {
	return fmt.Sprintf(
		`(() => {
			const button = document.querySelector(%q);
			if (!button) return false;
			const hasCheck = !!button.querySelector(".item-read-toggle-check");
			return button.dataset.readState === %q &&
				button.getAttribute("aria-label") === %q &&
				button.title === %q && hasCheck === %t;
		})()`,
		selector,
		state,
		action,
		action,
		checked,
	)
}

func itemReadToggleThemeExpression(selector, theme string) string {
	return fmt.Sprintf(
		`(() => {
			document.documentElement.setAttribute("data-theme", %q);
			const button = document.querySelector(%q);
			if (!button) return false;
			const rect = button.getBoundingClientRect();
			const style = getComputedStyle(button);
			return rect.width >= 32 && rect.height >= 32 &&
				style.display.endsWith("flex") && style.borderStyle === "solid" &&
				parseFloat(style.borderWidth) >= 1 && style.color !== "rgba(0, 0, 0, 0)";
		})()`,
		theme,
		selector,
	)
}

func armSourceLinkCapture(t *testing.T, ctx context.Context) {
	t.Helper()

	runActions(t, ctx, chromedp.Evaluate(`(() => {
		window.__pulseCapturedSourceHref = "";
		if (window.__pulseSourceCaptureBound) return true;
		window.__pulseSourceCaptureBound = true;
		document.addEventListener("click", (event) => {
			const target = event.target;
			const link = target && target.closest
				? target.closest("a[data-item-source-link='true']")
				: null;
			if (!link) return;
			event.preventDefault();
			window.__pulseCapturedSourceHref = link.href;
		}, true);
		return true;
	})()`, nil))
}

func sourceLinkCapturedExpression(selector string) string {
	return fmt.Sprintf(
		`(() => {
			const link = document.querySelector(%q);
			return !!link && window.__pulseCapturedSourceHref === link.href;
		})()`,
		selector,
	)
}

func missingClassExpression(selector, className string) string {
	return fmt.Sprintf(
		`(() => { const el = document.querySelector(%q); return !!el && !el.classList.contains(%q); })()`,
		selector,
		className,
	)
}

func activeElementMatchesExpression(selector string) string {
	return fmt.Sprintf(
		`(() => { const el = document.querySelector(%q); return !!el && document.activeElement === el; })()`,
		selector,
	)
}

func feedSelectionSettledExpression(feedID int64, feedTitle string) string {
	return fmt.Sprintf(
		`(() => {
			const feedID = %q;
			const active = document.querySelector("#feed-list .feed-link.active");
			const input = document.querySelector("#selected-feed-id");
			const list = document.querySelector("#item-list");
			const title = document.querySelector(".items-title");
			return !!active && !!input && !!list && !!title &&
				active.dataset.feedId === feedID &&
				input.value === feedID &&
				list.dataset.feedId === feedID &&
				title.textContent.trim() === %q;
		})()`,
		fmt.Sprintf("%d", feedID),
		feedTitle,
	)
}

func feedMoreExpandedExpression() string {
	return `(() => {
		const button = document.querySelector("#feed-list .feed-more-button");
		const zeroList = document.querySelector("#feed-list #feed-zero-list");
		if (!button || !zeroList) {
			return false;
		}
		return button.getAttribute("aria-expanded") === "true" &&
			zeroList.hidden === false &&
			window.getComputedStyle(zeroList).display !== "none";
	})()`
}

func feedMoreCollapsedExpression() string {
	return `(() => {
		const button = document.querySelector("#feed-list .feed-more-button");
		const zeroList = document.querySelector("#feed-list #feed-zero-list");
		if (!button || !zeroList) {
			return false;
		}
		return button.getAttribute("aria-expanded") === "false" &&
			zeroList.hidden === true &&
			window.getComputedStyle(zeroList).display === "none";
	})()`
}

func itemOutlineVisibleExpression(rowSelector string) string {
	return fmt.Sprintf(
		`(() => {
			const list = document.querySelector("#item-list");
			const row = document.querySelector(%q);
			if (!list || !row) {
				return false;
			}
			const boxShadow = window.getComputedStyle(row).boxShadow || "";
			return list.classList.contains("is-keyboard-nav") &&
				list.matches(":focus-within") &&
				boxShadow.includes("rgb(37, 99, 235)");
		})()`,
		rowSelector,
	)
}

func itemOutlineAbsentExpression(rowSelector string) string {
	return fmt.Sprintf(
		`(() => {
			const row = document.querySelector(%q);
			if (!row) {
				return false;
			}
			const boxShadow = window.getComputedStyle(row).boxShadow || "";
			return !boxShadow.includes("rgb(37, 99, 235)");
		})()`,
		rowSelector,
	)
}

func desktopLayoutExpression() string {
	return `(() => !window.matchMedia("(max-width: 960px)").matches)()`
}

func htmxReadyExpression() string {
	return `(() => !!window.htmx && typeof window.htmx.ajax === "function")()`
}

func htmxElementReadyExpression(selector string) string {
	return fmt.Sprintf(
		`(() => {
			if (document.querySelector(".htmx-request, .htmx-swapping, .htmx-settling")) {
				return false;
			}
			const el = document.querySelector(%q);
			if (!el) {
				return false;
			}
			if (!el.matches("[hx-get], [hx-post], [hx-put], [hx-delete], [hx-patch]")) {
				return true;
			}
			const data = el["htmx-internal-data"];
			return !!data && Array.isArray(data.listenerInfos) && data.listenerInfos.length > 0;
		})()`,
		selector,
	)
}

func htmxSettledExpression() string {
	return `(() => !document.querySelector(".htmx-request, .htmx-swapping, .htmx-settling"))()`
}

func mobileLayoutExpression() string {
	return `(() => window.matchMedia("(max-width: 960px)").matches)()`
}

func mobilePullRefreshStateExpression(state string) string {
	return fmt.Sprintf(
		`(() => {
			const stream = document.querySelector("[data-mobile-stream='true']");
			const indicator = stream ? stream.querySelector("[data-mobile-pull-refresh]") : null;
			const surface = stream ? stream.querySelector("#mobile-stream-content") : null;
			if (!stream || !indicator || !surface || indicator.dataset.state !== %q) {
				return false;
			}
			const distance = Number.parseFloat(surface.style.getPropertyValue("--mobile-pull-distance"));
			return Number.isFinite(distance) && distance > 0;
		})()`,
		state,
	)
}

func mobilePullRefreshDistanceAtLeastExpression(state string, minimum int) string {
	return fmt.Sprintf(
		`(() => {
			const stream = document.querySelector("[data-mobile-stream='true']");
			const indicator = stream ? stream.querySelector("[data-mobile-pull-refresh]") : null;
			const surface = stream ? stream.querySelector("#mobile-stream-content") : null;
			if (!stream || !indicator || !surface || indicator.dataset.state !== %q) {
				return false;
			}
			const distance = Number.parseFloat(surface.style.getPropertyValue("--mobile-pull-distance"));
			return Number.isFinite(distance) && distance >= %d;
		})()`,
		state,
		minimum,
	)
}

func mobilePullRootOverscrollBlockedExpression() string {
	return `(() =>
		getComputedStyle(document.documentElement).overscrollBehaviorY === "none" &&
		getComputedStyle(document.body).overscrollBehaviorY === "none"
	)()`
}

func mobilePullRefreshIdleExpression() string {
	return `(() => {
		const stream = document.querySelector("[data-mobile-stream='true']");
		const indicator = stream ? stream.querySelector("[data-mobile-pull-refresh]") : null;
		const surface = stream ? stream.querySelector("#mobile-stream-content") : null;
		const label = indicator ? indicator.querySelector("[data-mobile-pull-label]") : null;
		if (!stream || !indicator || !surface || !label) return false;
		const distance = Number.parseFloat(surface.style.getPropertyValue("--mobile-pull-distance") || "0");
		return indicator.dataset.state === "idle" &&
			label.textContent.trim() === "Pull to refresh" &&
			!stream.hasAttribute("data-mobile-pull-tracking") &&
			Number.isFinite(distance) &&
			distance <= 0;
	})()`
}

func mobilePullRefreshingExpression() string {
	return `(() => {
		const stream = document.querySelector("[data-mobile-stream='true']");
		const indicator = stream ? stream.querySelector("[data-mobile-pull-refresh]") : null;
		const surface = stream ? stream.querySelector("#mobile-stream-content") : null;
		const label = indicator ? indicator.querySelector("[data-mobile-pull-label]") : null;
		const announcement = indicator ? indicator.querySelector("[data-mobile-pull-announcement]") : null;
		if (!stream || !indicator || !surface || !label || !announcement) return false;
		const distance = Number.parseFloat(surface.style.getPropertyValue("--mobile-pull-distance"));
		return indicator.dataset.state === "refreshing" &&
			label.textContent.trim() === "Refreshing" &&
			announcement.textContent.trim().length > 0 &&
			Number.isFinite(distance) &&
			distance > 0;
	})()`
}

func mobilePullRefreshCompleteExpression() string {
	return `(() => {
		const stream = document.querySelector("[data-mobile-stream='true']");
		const indicator = stream ? stream.querySelector("[data-mobile-pull-refresh]") : null;
		const surface = stream ? stream.querySelector("#mobile-stream-content") : null;
		const announcement = indicator ? indicator.querySelector("[data-mobile-pull-announcement]") : null;
		if (!stream || !indicator || !surface || !announcement) return false;
		const distance = Number.parseFloat(surface.style.getPropertyValue("--mobile-pull-distance") || "0");
		return indicator.dataset.state === "idle" &&
			announcement.textContent.trim() === "Refresh complete." &&
			!stream.hasAttribute("data-mobile-pull-tracking") &&
			Number.isFinite(distance) &&
			distance <= 0;
	})()`
}

func mobilePullRefreshCanceledExpression() string {
	return `(() => {
		const stream = document.querySelector("[data-mobile-stream='true']");
		const indicator = stream ? stream.querySelector("[data-mobile-pull-refresh]") : null;
		const announcement = indicator ? indicator.querySelector("[data-mobile-pull-announcement]") : null;
		return !!stream && !!indicator && !!announcement &&
			indicator.dataset.state === "idle" &&
			announcement.textContent.trim() === "Refresh canceled." &&
			!stream.hasAttribute("data-mobile-pull-tracking");
	})()`
}

func mobilePullRefreshFailedExpression() string {
	return `(() => {
		const stream = document.querySelector("[data-mobile-stream='true']");
		const indicator = stream ? stream.querySelector("[data-mobile-pull-refresh]") : null;
		const surface = stream ? stream.querySelector("#mobile-stream-content") : null;
		const announcement = indicator ? indicator.querySelector("[data-mobile-pull-announcement]") : null;
		if (!stream || !indicator || !surface || !announcement) return false;
		const distance = Number.parseFloat(surface.style.getPropertyValue("--mobile-pull-distance") || "0");
		return indicator.dataset.state === "idle" &&
			announcement.textContent.trim() === "Refresh failed." &&
			!stream.hasAttribute("data-mobile-pull-tracking") &&
			Number.isFinite(distance) &&
			distance <= 0;
	})()`
}

func mobilePullReducedMotionExpression(state string) string {
	return fmt.Sprintf(
		`(() => {
			const stream = document.querySelector("[data-mobile-stream='true']");
			const indicator = stream ? stream.querySelector("[data-mobile-pull-refresh]") : null;
			const surface = stream ? stream.querySelector("#mobile-stream-content") : null;
			const icon = indicator ? indicator.querySelector(".mobile-pull-refresh-icon") : null;
			if (!stream || !indicator || !surface || !icon) return false;
			return matchMedia("(prefers-reduced-motion: reduce)").matches &&
				indicator.dataset.state === %q &&
				getComputedStyle(surface).transitionDuration === "0s" &&
				getComputedStyle(indicator).transitionDuration === "0s" &&
				getComputedStyle(icon).transitionDuration === "0s" &&
				getComputedStyle(icon).animationName === "none";
		})()`,
		state,
	)
}

func mobileDocumentScrollSurfaceExpression(itemID int64) string {
	itemHookCheck := "true"
	if itemID > 0 {
		itemHookCheck = fmt.Sprintf(
			`document.querySelector("#mobile-card-%d[data-mobile-item-id='%d']") !== null`,
			itemID,
			itemID,
		)
	}

	return fmt.Sprintf(
		`(() => {
			const root = document.scrollingElement;
			const body = document.body;
			const page = document.querySelector(".page");
			const app = document.querySelector(".app");
			const main = document.querySelector(".main-panel");
			if (!root || !body || !page || !app || !main) return false;
			const bodyStyle = getComputedStyle(body);
			const pageStyle = getComputedStyle(page);
			const appStyle = getComputedStyle(app);
			return window.matchMedia("(max-width: 960px)").matches &&
				root.scrollHeight > innerHeight &&
				bodyStyle.height !== innerHeight + "px" &&
				bodyStyle.overflowY === "visible" &&
				pageStyle.overflowY === "visible" &&
				appStyle.overflowY === "visible" &&
				getComputedStyle(main).overflowY === "visible" &&
				%s;
		})()`,
		itemHookCheck,
	)
}

func mobileDocumentScrolledExpression() string {
	return `(() => {
		const app = document.querySelector(".app");
		const topbar = document.querySelector(".topbar");
		const actions = document.querySelector(".mobile-reader-actions");
		if (!app || !topbar || window.scrollY <= 0 || app.scrollTop !== 0) return false;
		const topbarRect = topbar.getBoundingClientRect();
		if (Math.abs(topbarRect.top) > 1) return false;
		if (!actions) return true;
		const actionsRect = actions.getBoundingClientRect();
		return actionsRect.top >= 0 && actionsRect.bottom <= innerHeight;
	})()`
}

func mobileSelectionFailurePreservesScrollExpression() string {
	return `(() => {
		const app = document.querySelector(".app");
		const root = document.scrollingElement;
		return !!app && !!root &&
			!!document.querySelector("[data-mobile-stream='true'] [data-mobile-sections-page]") &&
			location.pathname + location.search === "/mobile/stream" &&
			Number.isFinite(window.__mobileSelectionScrollY) &&
			Math.abs(window.scrollY - window.__mobileSelectionScrollY) <= 1 &&
			Math.abs(root.scrollTop - window.__mobileSelectionScrollY) <= 1 &&
			app.scrollTop === 0;
	})()`
}

func mobileStreamAtTrueTopExpression(feedID int64) string {
	return fmt.Sprintf(
		`(() => {
			const app = document.querySelector(".app");
			const root = document.scrollingElement;
			const filter = document.querySelector("#mobile-stream-feed-filter");
			return !!app && !!root && !!filter &&
				!!document.querySelector("[data-mobile-stream='true']") &&
				filter.value === %q &&
				Math.abs(window.scrollY) <= 1 &&
				Math.abs(root.scrollTop) <= 1 &&
				Math.abs(app.scrollTop) <= 1;
		})()`,
		strconv.FormatInt(feedID, 10),
	)
}

func desktopPanelScrollSurfaceExpression() string {
	return `(() => {
		const body = document.body;
		const page = document.querySelector(".page");
		const app = document.querySelector(".app");
		const feed = document.querySelector(".feed-panel");
		const main = document.querySelector(".main-panel");
		if (!body || !page || !app || !feed || !main) return false;
		return !window.matchMedia("(max-width: 960px)").matches &&
			getComputedStyle(body).overflowY === "hidden" &&
			getComputedStyle(page).overflowY === "hidden" &&
			getComputedStyle(app).overflowY === "hidden" &&
			getComputedStyle(feed).overflowY === "auto" &&
			getComputedStyle(main).overflowY === "auto";
	})()`
}

func mobileCardAtViewportOffsetExpression(itemID int64, offset int) string {
	return fmt.Sprintf(
		`(() => {
			const card = document.querySelector("#mobile-card-%d");
			return !!card && window.scrollY > 0 &&
				Math.abs(card.getBoundingClientRect().top - %d) <= 2;
		})()`,
		itemID,
		offset,
	)
}

func mobileReaderOriginStoredExpression(itemID int64, cardIndex int, previousItemID, nextItemID int64) string {
	return fmt.Sprintf(
		`(() => {
			let record;
			try {
				record = JSON.parse(sessionStorage.getItem("pulse.mobileReaderOrigin.v1"));
			} catch (_error) {
				return false;
			}
			const state = history.state;
			return !!record && !!state && state.htmx === true &&
				typeof state.pulseMobileReaderNavigationID === "string" &&
				state.pulseMobileReaderNavigationID === record.navigationID &&
				record.version === 1 &&
				record.streamURL === "/mobile/stream" &&
				record.readerRequestPath === location.pathname + location.search &&
				record.itemID === %q &&
				record.cardIndex === %d &&
				record.previousItemID === %q &&
				record.nextItemID === %q &&
				record.scrollY > 0 &&
				Number.isFinite(record.cardViewportOffset);
		})()`,
		fmt.Sprintf("%d", itemID),
		cardIndex,
		fmt.Sprintf("%d", previousItemID),
		fmt.Sprintf("%d", nextItemID),
	)
}

func mobileReaderOriginRestoredExpression(itemID int64) string {
	return fmt.Sprintf(
		`(() => {
			let record;
			try {
				record = JSON.parse(sessionStorage.getItem("pulse.mobileReaderOrigin.v1"));
			} catch (_error) {
				return false;
			}
			const card = document.querySelector("#mobile-card-%d");
			return !!record && !!card &&
				!!document.querySelector("[data-mobile-stream='true']") &&
				location.pathname + location.search === record.streamURL &&
				record.itemID === %q &&
				window.scrollY > 0 &&
				Math.abs(card.getBoundingClientRect().top - record.cardViewportOffset) <= 2;
		})()`,
		itemID,
		fmt.Sprintf("%d", itemID),
	)
}

func mobileReaderOriginRestoredAtTopExpression(itemID int64) string {
	return fmt.Sprintf(
		`(() => {
			let record;
			try {
				record = JSON.parse(sessionStorage.getItem("pulse.mobileReaderOrigin.v1"));
			} catch (_error) {
				return false;
			}
			const card = document.querySelector("#mobile-card-%d");
			return !!record && !!card &&
				!!document.querySelector("[data-mobile-stream='true']") &&
				location.pathname + location.search === record.streamURL &&
				record.itemID === %q &&
				Math.abs(card.getBoundingClientRect().top - record.cardViewportOffset) <= 2;
		})()`,
		itemID,
		fmt.Sprintf("%d", itemID),
	)
}

func mobileReaderNavigationStateExpression(itemID int64) string {
	return fmt.Sprintf(
		`(() => {
			let record;
			try {
				record = JSON.parse(sessionStorage.getItem("pulse.mobileReaderOrigin.v1"));
			} catch (_error) {
				return false;
			}
			const state = history.state;
			return !!record && !!state && state.htmx === true &&
				!!document.querySelector("[data-mobile-reader='true']") &&
				record.itemID === %q &&
				record.readerRequestPath === location.pathname + location.search &&
				state.pulseMobileReaderNavigationID === record.navigationID;
		})()`,
		fmt.Sprintf("%d", itemID),
	)
}

func mobileReaderOriginStashedExpression(itemID, nextItemID int64, cardIndex int) string {
	return fmt.Sprintf(
		`(() => {
			let record;
			try {
				record = JSON.parse(sessionStorage.getItem("pulse.mobileReaderOrigin.v1"));
			} catch (_error) {
				return false;
			}
			const state = history.state;
			if (
				!record ||
				!state ||
				state.htmx !== true ||
				state.pulseMobileReaderNavigationID !== record.navigationID ||
				record.itemID !== %q ||
				record.nextItemID !== %q ||
				record.cardIndex !== %d ||
				!Number.isFinite(record.cardViewportOffset)
			) {
				return false;
			}
			window.__mobileMarkReadOrigin = { ...record };
			return true;
		})()`,
		fmt.Sprintf("%d", itemID),
		itemIDString(nextItemID),
		cardIndex,
	)
}

func mobileReaderMarkReadContinuationExpression(removedItemID, targetItemID int64) string {
	return fmt.Sprintf(
		`(() => {
			const record = window.__mobileMarkReadOrigin;
			const stream = document.querySelector("[data-mobile-stream='true']");
			const removed = document.querySelector("#mobile-card-%d");
			const target = document.querySelector("#mobile-card-%d");
			const root = document.scrollingElement;
			if (!record || !stream || removed || !target || !root) return false;
			if (stream.getAnimations().some((animation) => animation.playState === "running")) {
				return false;
			}
			let documentTop = 0;
			for (let current = target; current; current = current.offsetParent) {
				documentTop += current.offsetTop;
			}
			const maximum = Math.max(0, root.scrollHeight - innerHeight);
			const expectedScroll = Math.min(
				maximum,
				Math.max(0, documentTop - record.cardViewportOffset),
			);
			const expectedViewportTop = documentTop - expectedScroll;
			const state = history.state;
			return Math.abs(window.scrollY - expectedScroll) <= 2 &&
				Math.abs(target.getBoundingClientRect().top - expectedViewportTop) <= 2 &&
				sessionStorage.getItem("pulse.mobileReaderOrigin.v1") === null &&
				(!state || !state.pulseMobileReaderNavigationID);
		})()`,
		removedItemID,
		targetItemID,
	)
}

func mobileReaderEmptyMarkReadContinuationExpression(removedItemID int64) string {
	return fmt.Sprintf(
		`(() => {
			const stream = document.querySelector("[data-mobile-stream='true']");
			const state = history.state;
			return !!stream &&
				!document.querySelector("#mobile-card-%d") &&
				!stream.querySelector("[data-mobile-item-id]") &&
				!!stream.querySelector("[data-mobile-empty='true']") &&
				Math.abs(window.scrollY) <= 1 &&
				sessionStorage.getItem("pulse.mobileReaderOrigin.v1") === null &&
				(!state || !state.pulseMobileReaderNavigationID);
		})()`,
		removedItemID,
	)
}

func mobileReaderMarkReadFailurePreservesOriginExpression(itemID int64) string {
	return fmt.Sprintf(
		`(() => {
			let stored;
			try {
				stored = JSON.parse(sessionStorage.getItem("pulse.mobileReaderOrigin.v1"));
			} catch (_error) {
				return false;
			}
			const stashed = window.__mobileMarkReadOrigin;
			const state = history.state;
			const markRead = document.querySelector(
				".mobile-reader-mark-read[hx-post^='/mobile/items/%d/read']",
			);
			return !!stored && !!stashed && !!state && !!markRead &&
				!!document.querySelector("[data-mobile-reader='true']") &&
				!document.querySelector("[data-mobile-stream='true']") &&
				stored.itemID === %q &&
				stored.navigationID === stashed.navigationID &&
				state.htmx === true &&
				state.pulseMobileReaderNavigationID === stored.navigationID;
		})()`,
		itemID,
		fmt.Sprintf("%d", itemID),
	)
}

func itemIDString(itemID int64) string {
	if itemID <= 0 {
		return ""
	}
	return strconv.FormatInt(itemID, 10)
}

func windowScrollYExpression(scrollY int) string {
	return fmt.Sprintf(`(() => Math.abs(window.scrollY - %d) <= 1)()`, scrollY)
}

func failedReaderPreservesStreamExpression() string {
	return `(() => {
		const state = history.state;
		return !!document.querySelector("[data-mobile-stream='true']") &&
			location.pathname === "/mobile/stream" &&
			Math.abs(window.scrollY - window.__failedReaderScrollY) <= 1 &&
			(!state || !state.pulseMobileReaderNavigationID) &&
			sessionStorage.getItem("pulse.mobileReaderOrigin.v1") === null;
	})()`
}

func directReaderHasNoOriginExpression() string {
	return `(() => {
		const state = history.state;
		return !!document.querySelector("[data-mobile-reader='true']") &&
			(!state || !state.pulseMobileReaderNavigationID);
	})()`
}

func mobileAggregateSectionOrderExpression(feedIDs ...int64) string {
	parts := make([]string, 0, len(feedIDs))

	for _, feedID := range feedIDs {
		parts = append(parts, strconv.FormatInt(feedID, 10))
	}
	expected := strings.Join(parts, ",")

	return fmt.Sprintf(
		`(() => Array.from(document.querySelectorAll("[data-mobile-feed-section][data-feed-id]"))
			.map((section) => section.dataset.feedId).join(",") === %q)()`,
		expected,
	)
}

func mobileAggregateCompactSectionsExpression() string {
	return `(() => {
		const list = document.querySelector(".mobile-stream-sections");
		const sections = Array.from(document.querySelectorAll("[data-mobile-feed-section]"));
		if (!list || sections.length < 2 || document.querySelector(".mobile-feed-section-header")) {
			return false;
		}
		if (window.getComputedStyle(list).gap !== "0px") {
			return false;
		}
		const firstBoundary = window.getComputedStyle(sections[0], "::before");
		const secondSection = window.getComputedStyle(sections[1]);
		const secondBoundary = window.getComputedStyle(sections[1], "::before");
		if (
			firstBoundary.content !== "none" ||
			(secondBoundary.content !== '""' && secondBoundary.content !== "''") ||
			secondSection.marginTop !== "16px" ||
			secondSection.paddingTop !== "12px" ||
			secondBoundary.top !== "0px" ||
			secondBoundary.width !== "36px" ||
			secondBoundary.height !== "3px" ||
			secondBoundary.backgroundColor === "rgba(0, 0, 0, 0)"
		) {
			return false;
		}
		return sections.every((section) => {
			const sectionStyle = window.getComputedStyle(section);
			const card = section.querySelector(".mobile-card");
			const source = card && card.querySelector(".mobile-card-source");
			return section.getAttribute("aria-label") &&
				sectionStyle.borderTopStyle === "none" &&
				card &&
				window.getComputedStyle(card).borderBottomStyle !== "none" &&
				source &&
				source.textContent.trim();
		});
	})()`
}

func mobileAggregateBoundedExpression(maxSections, maxItemsPerSection int) string {
	return fmt.Sprintf(
		`(() => {
			const sections = Array.from(document.querySelectorAll("[data-mobile-feed-section]"));
			return sections.length <= %d && sections.every((section) =>
				section.querySelectorAll("[data-mobile-item-id]").length <= %d
			);
		})()`,
		maxSections,
		maxItemsPerSection,
	)
}

func responsiveMobileLayoutExpression(feedID int64) string {
	return fmt.Sprintf(
		`(() => {
			const content = document.querySelectorAll(
				'#main-content [data-mobile-stream="true"], #main-content [data-mobile-reader="true"]',
			);
			const filters = document.querySelectorAll("#mobile-stream-feed-filter");
			const filter = filters[0];
			return window.matchMedia("(max-width: 960px)").matches &&
				content.length === 1 && filters.length === 1 &&
				filter.getClientRects().length > 0 && !filter.disabled &&
				filter.options.length > 0 && filter.value === %q;
		})()`,
		fmt.Sprintf("%d", feedID),
	)
}

func responsiveDesktopLayoutExpression(feedID int64) string {
	return fmt.Sprintf(
		`(() => {
			const list = document.querySelector("#item-list");
			const feedPanel = document.querySelector(".feed-panel");
			const mainPanel = document.querySelector(".main-panel");
			const contentPanel = document.querySelector("#content-panel");
			const brand = document.querySelector('#topbar-brand-button[hx-post="/feeds/pulse"]');
			const mobileSlot = document.querySelector("#topbar-mobile-slot");
			return !window.matchMedia("(max-width: 960px)").matches &&
				!!list && list.dataset.feedId === %q &&
				feedPanel.getClientRects().length > 0 && mainPanel.getClientRects().length > 0 &&
				!!contentPanel && !!brand && !!mobileSlot && !mobileSlot.classList.contains("is-active") &&
				!document.querySelector("#main-content [data-mobile-stream], #main-content [data-mobile-reader]") &&
				!document.querySelector("#mobile-stream-feed-filter");
		})()`,
		fmt.Sprintf("%d", feedID),
	)
}

func textPresentExpression(text string) string {
	return fmt.Sprintf(
		`(() => document.body && document.body.textContent && document.body.textContent.includes(%q))()`,
		text,
	)
}

func textAbsentExpression(text string) string {
	return fmt.Sprintf(
		`(() => !document.body || !document.body.textContent || !document.body.textContent.includes(%q))()`,
		text,
	)
}

func contentPanelItemExpression(itemID int64) string {
	return fmt.Sprintf(
		`(() => {
			const panel = document.querySelector("#content-panel.is-open");
			if (!panel) {
				return false;
			}
			const article = panel.querySelector(".content-panel-article[data-item-id]");
			return !!article && article.getAttribute("data-item-id") === %q;
		})()`,
		fmt.Sprintf("%d", itemID),
	)
}

func dockedPanelsScrollableExpression(itemID int64) string {
	return fmt.Sprintf(
		`(() => {
			const app = document.querySelector(".app");
			const main = document.querySelector(".main-panel");
			const panel = document.querySelector("#content-panel.is-open");
			const article = panel && panel.querySelector(".content-panel-article[data-item-id]");
			if (!app || !main || !panel || !article ||
				app.classList.contains("is-content-panel-floating") ||
				article.getAttribute("data-item-id") !== %q ||
				getComputedStyle(main).overflowY !== "auto" ||
				getComputedStyle(panel).overflowY !== "auto" ||
				main.scrollHeight <= main.clientHeight || panel.scrollHeight <= panel.clientHeight) {
				return false;
			}
			panel.scrollTop = Math.min(180, panel.scrollHeight - panel.clientHeight);
			window.__dockedReaderScrollTop = panel.scrollTop;
			return panel.scrollTop > 0;
		})()`,
		fmt.Sprintf("%d", itemID),
	)
}

func dockedItemsWheelScrollExpression(itemID int64) string {
	return fmt.Sprintf(
		`(() => {
			const main = document.querySelector(".main-panel");
			const panel = document.querySelector("#content-panel.is-open");
			const article = panel && panel.querySelector(".content-panel-article[data-item-id]");
			return !!main && !!panel && !!article && main.scrollTop > 0 &&
				article.getAttribute("data-item-id") === %q &&
				Math.abs(panel.scrollTop - window.__dockedReaderScrollTop) <= 1;
		})()`,
		fmt.Sprintf("%d", itemID),
	)
}

func floatingBackdropCoversControlExpression(selector string) string {
	return fmt.Sprintf(
		`(() => {
			const app = document.querySelector(".app.is-content-panel-floating");
			const panel = document.querySelector("#content-panel.is-open");
			const control = document.querySelector(%q);
			if (!app || !panel || !control) return false;
			control.scrollIntoView({ block: "center" });
			const controlRect = control.getBoundingClientRect();
			const panelRect = panel.getBoundingClientRect();
			const controlX = controlRect.left + controlRect.width / 2;
			const controlY = controlRect.top + controlRect.height / 2;
			return controlRect.width > 0 && controlRect.height > 0 &&
				controlX >= 0 && controlX <= innerWidth && controlY >= 0 && controlY <= innerHeight &&
				(controlX < panelRect.left || controlX > panelRect.right ||
					controlY < panelRect.top || controlY > panelRect.bottom);
		})()`,
		selector,
	)
}

func itemReadStateExpression(rowSelector, state string) string {
	return fmt.Sprintf(
		`(() => {
			const row = document.querySelector(%q);
			const toggle = row && row.querySelector(".item-read-toggle");
			return !!toggle && toggle.dataset.readState === %q;
		})()`,
		rowSelector,
		state,
	)
}

func assertPanelResize(
	t *testing.T,
	ctx context.Context,
	resizerSelector string,
	cssProperty string,
	storageKey string,
	pointerDelta int,
) {
	t.Helper()

	waitForJS(
		t,
		ctx,
		fmt.Sprintf(
			`(() => {
				const resizer = document.querySelector(%q);
				return !!resizer && resizer.dataset.bound === "true";
			})()`,
			resizerSelector,
		),
		"panel resizer bound",
	)

	expression := fmt.Sprintf(
		`(() => {
			const resizer = document.querySelector(%q);
			const panel = %q === "--feed-panel-width"
				? document.querySelector(".feed-panel")
				: document.querySelector("#content-panel");
			if (!resizer || !panel || resizer.dataset.bound !== "true") {
				return { ok: false, error: "missing panel or binding" };
			}

			const rawWidth = Number.parseFloat(
				getComputedStyle(document.documentElement).getPropertyValue(%q)
			);
			const before = Number.isFinite(rawWidth)
				? rawWidth
				: panel.getBoundingClientRect().width;
			Object.defineProperty(resizer, "setPointerCapture", {
				configurable: true,
				value: () => {},
			});
			resizer.dispatchEvent(new PointerEvent("pointerdown", {
				bubbles: true,
				button: 0,
				clientX: 400,
				pointerId: 41,
			}));
			resizer.dispatchEvent(new PointerEvent("pointermove", {
				bubbles: true,
				clientX: 400 + %d,
				pointerId: 41,
			}));
			resizer.dispatchEvent(new PointerEvent("pointerup", {
				bubbles: true,
				clientX: 400 + %d,
				pointerId: 41,
			}));

			const stored = Number.parseFloat(localStorage.getItem(%q) || "");
			const applied = Number.parseFloat(
				document.documentElement.style.getPropertyValue(%q)
			);
			const direction = Math.sign(%q === "--feed-panel-width" ? %d : -(%d));
			const ok = Number.isFinite(stored) && Number.isFinite(applied) &&
				(applied - before) * direction > 0 && Math.abs(stored - applied) <= 1 &&
				!document.body.classList.contains("is-resizing-feed-panel") &&
				!document.body.classList.contains("is-resizing-content-panel");
			return { ok, before, applied, stored, direction };
		})()`,
		resizerSelector,
		cssProperty,
		cssProperty,
		pointerDelta,
		pointerDelta,
		storageKey,
		cssProperty,
		cssProperty,
		pointerDelta,
		pointerDelta,
	)

	var result struct {
		OK        bool    `json:"ok"`
		Error     string  `json:"error"`
		Before    float64 `json:"before"`
		Applied   float64 `json:"applied"`
		Stored    float64 `json:"stored"`
		Direction float64 `json:"direction"`
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &result)); err != nil {
		t.Fatalf("resize %s: %v", cssProperty, err)
	}
	if !result.OK {
		t.Fatalf(
			"resize %s failed: error=%q before=%.1f applied=%.1f stored=%.1f direction=%.0f",
			cssProperty,
			result.Error,
			result.Before,
			result.Applied,
			result.Stored,
			result.Direction,
		)
	}
}

func mobileReaderLargeTextFitsExpression() string {
	return `(() => {
		const article = document.querySelector(".mobile-reader-article");
		const source = document.querySelector(".mobile-reader-source");
		const title = document.querySelector(".mobile-reader-title");
		const body = document.querySelector(".mobile-reader-body");
		if (!article || !source || !title || !body) {
			return false;
		}

		source.style.fontSize = "22px";
		title.style.fontSize = "50px";
		body.style.fontSize = "32px";
		const wraps = [source, title, body].every(
			(element) => getComputedStyle(element).overflowWrap === "anywhere"
		);
		return wraps &&
			document.documentElement.scrollWidth <= window.innerWidth + 1 &&
			article.scrollWidth <= article.clientWidth + 1;
	})()`
}

func feedUnreadCountExpression(feedID int64, want string) string {
	return fmt.Sprintf(
		`(() => {
			const feed = document.querySelector(%q);
			if (!feed) {
				return false;
			}
			const count = feed.querySelector(".feed-count");
			return !!count && count.textContent.trim() === %q;
		})()`,
		fmt.Sprintf(`#feed-list .feed-link[data-feed-id="%d"]`, feedID),
		want,
	)
}

func desktopPulseIndicatorsExpression(fixture smokeFixture) string {
	return fmt.Sprintf(
		`(() => {
			const checks = [
				[%q, "fresh", "Fresh"],
				[%q, "pending", "Refreshing"],
				[%q, "error", "Refresh failed"],
			];
			if (document.documentElement.scrollWidth > window.innerWidth + 1) {
				return false;
			}
			return checks.every(([feedID, className, label]) => {
				const feed = document.querySelector(
					'#feed-list .feed-link[data-feed-id="' + feedID + '"]'
				);
				const indicator = feed && feed.querySelector(".feed-pulse-indicator." + className);
				if (!feed || !indicator || indicator.getAttribute("role") !== "img" ||
					indicator.getAttribute("aria-label") !== label) {
					return false;
				}
				const style = window.getComputedStyle(feed);
				const columns = style.gridTemplateColumns.split(" ");
				return columns.length === 3 && Math.round(parseFloat(columns[0])) === 10;
			});
		})()`,
		fmt.Sprintf("%d", fixture.primaryFeedID),
		fmt.Sprintf("%d", fixture.secondaryFeedID),
		fmt.Sprintf("%d", fixture.tertiaryFeedID),
	)
}

func mobileFlatStreamLayoutExpression(fixture smokeFixture) string {
	return fmt.Sprintf(
		`(() => {
			const panel = document.querySelector(".mobile-feed-status-panel");
			const refreshRow = document.querySelector(".mobile-stream-refresh-row");
			const refreshButton = document.querySelector(".mobile-stream-refresh-button");
			const brandButton = document.querySelector("#topbar-brand-button");
			const list = document.querySelector(".mobile-stream-list");
			const card = document.querySelector(".mobile-card");
			const titleRow = card && card.querySelector(".mobile-card-title-row");
			const openButton = card && card.querySelector(".mobile-card-open");
			const markRead = card && card.querySelector(".mobile-card-mark-read");
			if (panel || refreshRow || refreshButton || !brandButton || !list || !card ||
				!titleRow || !openButton || !markRead || document.documentElement.scrollWidth > window.innerWidth + 1) {
				return false;
			}
			const hxPost = brandButton.getAttribute("hx-post") || "";
			if (!hxPost.includes(%q) || !hxPost.includes(%q)) {
				return false;
			}
			if (brandButton.getAttribute("aria-label") !== "Refresh feed Secondary Feed") {
				return false;
			}
			if (brandButton.getAttribute("hx-indicator") !== "#topbar-brand-button") {
				return false;
			}
			const pending = brandButton.querySelector(".brand-subtitle-pending");
			if (!pending || pending.textContent.trim() !== "Refreshing Secondary Feed") {
				return false;
			}
			const listStyle = window.getComputedStyle(list);
			const cardStyle = window.getComputedStyle(card);
			if (listStyle.borderTopStyle === "none" || cardStyle.borderBottomStyle === "none" ||
				cardStyle.borderRadius !== "0px" || cardStyle.boxShadow !== "none") {
				return false;
			}
			const titleRect = openButton.getBoundingClientRect();
			const markRect = markRead.getBoundingClientRect();
			if (markRect.left <= titleRect.right || markRect.width < 34 || markRect.height < 34) {
				return false;
			}
			return Boolean(markRead.querySelector("svg.icon")) && markRect.right <= window.innerWidth + 1;
		})()`,
		fmt.Sprintf("/mobile/feeds/%d/refresh", fixture.secondaryFeedID),
		fmt.Sprintf("%d", fixture.secondaryFeedID),
	)
}

func mobileCardCompactPreviewExpression(itemID int64, previewText string) string {
	return fmt.Sprintf(
		`(() => {
			const button = document.querySelector(%q);
			if (!button) {
				return false;
			}
			const card = button.closest(".mobile-card");
			if (!card) {
				return false;
			}
			const summary = card.querySelector(".mobile-card-summary");
			if (!summary) {
				return false;
			}
			const text = (summary.textContent || "").replace(/\s+/g, " ").trim();
			return text === %q && !summary.querySelector("h1, h2, h3, img");
		})()`,
		fmt.Sprintf(`.mobile-card-open[hx-get^="/mobile/items/%d/reader"]`, itemID),
		previewText,
	)
}
