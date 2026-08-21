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

    document.addEventListener("DOMContentLoaded", initializeMobileNavigation);
})();
