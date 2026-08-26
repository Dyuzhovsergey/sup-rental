(function () {
    "use strict";

    const storageKey = "sup-rental-theme";
    const darkTheme = "dark";
    const lightTheme = "light";
    const root = document.documentElement;
    const systemTheme = window.matchMedia("(prefers-color-scheme: dark)");

    root.classList.add("js");

    function storedTheme() {
        try {
            const value = window.localStorage.getItem(storageKey);
            return value === darkTheme || value === lightTheme ? value : "";
        } catch (_error) {
            return "";
        }
    }

    function preferredTheme() {
        return systemTheme.matches ? darkTheme : lightTheme;
    }

    function updateToggle(toggle, theme) {
        const isDark = theme === darkTheme;
        const nextThemeLabel = isDark ? "Светлая тема" : "Тёмная тема";
        const accessibleName = isDark ? "Включить светлую тему" : "Включить тёмную тему";

        toggle.setAttribute("aria-pressed", String(isDark));
        toggle.setAttribute("aria-label", accessibleName);
        toggle.setAttribute("title", accessibleName);

        const label = toggle.querySelector("[data-theme-label]");
        if (label) {
            label.textContent = nextThemeLabel;
        }

    }

    function updateToggles(theme) {
        document.querySelectorAll("[data-theme-toggle]").forEach(function (toggle) {
            updateToggle(toggle, theme);
        });
    }

    function applyTheme(theme, persist) {
        root.dataset.theme = theme;
        root.style.colorScheme = theme;

        if (persist) {
            try {
                window.localStorage.setItem(storageKey, theme);
            } catch (_error) {
                // The selected theme still applies for the current page.
            }
        }

        updateToggles(theme);
    }

    applyTheme(storedTheme() || preferredTheme(), false);

    document.addEventListener("DOMContentLoaded", function () {
        updateToggles(root.dataset.theme);

        document.querySelectorAll("[data-theme-toggle]").forEach(function (toggle) {
            toggle.addEventListener("click", function () {
                const nextTheme = root.dataset.theme === darkTheme ? lightTheme : darkTheme;
                applyTheme(nextTheme, true);
            });
        });
    });

    systemTheme.addEventListener("change", function () {
        if (!storedTheme()) {
            applyTheme(preferredTheme(), false);
        }
    });

    window.addEventListener("storage", function (event) {
        if (event.key !== storageKey) {
            return;
        }
        applyTheme(storedTheme() || preferredTheme(), false);
    });

    function initializeNavigableRows() {
        const interactiveSelector = [
            "a",
            "button",
            "input",
            "select",
            "textarea",
            "label",
            "summary",
            "[contenteditable]",
            "[data-no-row-navigation]",
            "[role='button']",
            "[role='link']",
            "[role='menuitem']",
            "[role='checkbox']",
            "[role='radio']",
            "[role='switch']",
            "[role='tab']",
        ].join(",");

        function hasInteractiveTarget(event, row) {
            const interactive = event.target.closest(interactiveSelector);
            return interactive && interactive !== row;
        }

        function navigate(row) {
            const href = row.dataset.rowHref;
            if (href) {
                window.location.assign(href);
            }
        }

        document.querySelectorAll("[data-row-href]").forEach(function (row) {
            row.addEventListener("click", function (event) {
                if (event.defaultPrevented || event.button !== 0 || hasInteractiveTarget(event, row)) {
                    return;
                }

                const selection = window.getSelection();
                if (selection && !selection.isCollapsed) {
                    return;
                }

                navigate(row);
            });

            row.addEventListener("keydown", function (event) {
                if (event.key !== "Enter" || event.target !== row) {
                    return;
                }

                event.preventDefault();
                navigate(row);
            });
        });
    }

    function initializeMobileNavigation() {
        const navigation = document.querySelector("[data-mobile-nav]");
        const openButton = document.querySelector("[data-mobile-nav-open]");
        const closeButtons = document.querySelectorAll("[data-mobile-nav-close]");
        const backdrop = document.querySelector(".mobile-nav-backdrop");
        const main = document.querySelector("#main-content");
        const mobileViewport = window.matchMedia("(max-width: 1023px)");

        if (!navigation || !openButton || !backdrop) {
            return;
        }

        function setMainInert(inert) {
            if (!main) {
                return;
            }
            main.inert = inert;
        }

        function closeNavigation(returnFocus) {
            navigation.classList.remove("is-open");
            document.body.classList.remove("mobile-nav-open");
            openButton.setAttribute("aria-expanded", "false");
            backdrop.hidden = true;
            setMainInert(false);

            if (mobileViewport.matches) {
                navigation.setAttribute("aria-hidden", "true");
            } else {
                navigation.removeAttribute("aria-hidden");
            }

            if (returnFocus && mobileViewport.matches) {
                openButton.focus();
            }
        }

        function openNavigation() {
            if (!mobileViewport.matches) {
                return;
            }

            navigation.classList.add("is-open");
            navigation.setAttribute("aria-hidden", "false");
            document.body.classList.add("mobile-nav-open");
            openButton.setAttribute("aria-expanded", "true");
            backdrop.hidden = false;
            setMainInert(true);

            const closeButton = navigation.querySelector("[data-mobile-nav-close]");
            if (closeButton) {
                closeButton.focus();
            }
        }

        openButton.addEventListener("click", openNavigation);
        closeButtons.forEach(function (button) {
            button.addEventListener("click", function () {
                closeNavigation(true);
            });
        });

        navigation.addEventListener("click", function (event) {
            if (event.target.closest("a")) {
                closeNavigation(false);
            }
        });

        document.addEventListener("keydown", function (event) {
            if (event.key === "Escape" && navigation.classList.contains("is-open")) {
                closeNavigation(true);
            }
        });

        mobileViewport.addEventListener("change", function () {
            closeNavigation(false);
        });

        closeNavigation(false);
    }

    function initializeOperatorMonitoring() {
        const timings = Array.from(document.querySelectorAll("[data-operator-timing]"));
        const refreshIntervalMilliseconds = 30000;
        let timerID = 0;

        if (timings.length === 0) {
            return;
        }

        function russianWord(value, one, few, many) {
            const lastTwoDigits = value % 100;
            const lastDigit = value % 10;
            if (lastDigit === 1 && lastTwoDigits !== 11) {
                return one;
            }
            if (lastDigit >= 2 && lastDigit <= 4 && (lastTwoDigits < 12 || lastTwoDigits > 14)) {
                return few;
            }
            return many;
        }

        function durationLabel(milliseconds) {
            const minutes = Math.ceil(Math.abs(milliseconds) / 60000);
            if (minutes <= 0) {
                return "менее минуты";
            }

            const days = Math.floor(minutes / (24 * 60));
            const hours = Math.floor((minutes % (24 * 60)) / 60);
            const remainingMinutes = minutes % 60;
            const parts = [];
            if (days > 0) {
                parts.push(days + " " + russianWord(days, "день", "дня", "дней"));
            }
            if (hours > 0) {
                parts.push(hours + " " + russianWord(hours, "час", "часа", "часов"));
            }
            if (remainingMinutes > 0) {
                parts.push(remainingMinutes + " мин");
            }
            return parts.join(" ");
        }

        function progressPercent(start, end, now) {
            if (now <= start) {
                return 0;
            }
            if (now >= end || end <= start) {
                return 100;
            }
            return Math.floor((now - start) * 100 / (end - start));
        }

        function updateTiming(timing, now) {
            const start = Number(timing.dataset.startUnixMs);
            const end = Number(timing.dataset.endUnixMs);
            const status = timing.dataset.rentalStatus;
            const rentalID = timing.dataset.rentalId;
            const label = timing.querySelector("[data-operator-timing-label]");
            const progress = timing.querySelector("progress");
            let text;
            let tone;

            if (!Number.isFinite(start) || !Number.isFinite(end) || !label || !progress) {
                return;
            }

            const percent = progressPercent(start, end, now);
            if (now < start) {
                text = "До начала " + durationLabel(start - now);
                tone = "neutral";
            } else if (status === "confirmed") {
                text = "Выдача задерживается на " + durationLabel(now - start);
                tone = "warning";
            } else if (now === end) {
                text = "Плановое время завершения наступило";
                tone = "warning";
            } else if (now > end) {
                text = "Просрочена на " + durationLabel(now - end);
                tone = "danger";
            } else {
                text = "Осталось " + durationLabel(end - now);
                tone = "success";
            }

            timing.className = "operator-timing operator-timing--" + tone;
            label.textContent = text;
            progress.value = percent;
            progress.textContent = percent + "%";
            progress.setAttribute("aria-label", "Период аренды №" + rentalID + ": " + percent + "%");
        }

        function updateAll() {
            const now = Date.now();
            timings.forEach(function (timing) {
                updateTiming(timing, now);
            });
        }

        function stopTimer() {
            if (timerID !== 0) {
                window.clearInterval(timerID);
                timerID = 0;
            }
        }

        function startTimer() {
            updateAll();
            if (timerID === 0) {
                timerID = window.setInterval(updateAll, refreshIntervalMilliseconds);
            }
        }

        document.addEventListener("visibilitychange", function () {
            if (document.visibilityState === "visible") {
                startTimer();
            } else {
                stopTimer();
            }
        });

        if (document.visibilityState === "visible") {
            startTimer();
        }
    }

    document.addEventListener("DOMContentLoaded", initializeMobileNavigation);
    document.addEventListener("DOMContentLoaded", initializeNavigableRows);
    document.addEventListener("DOMContentLoaded", initializeOperatorMonitoring);
})();
