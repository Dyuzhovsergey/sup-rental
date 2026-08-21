(function () {
    "use strict";

    const storageKey = "sup-rental-theme";
    const darkTheme = "dark";
    const lightTheme = "light";
    const root = document.documentElement;
    const systemTheme = window.matchMedia("(prefers-color-scheme: dark)");

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

        const lightIcon = toggle.querySelector('[data-theme-icon="light"]');
        const darkIcon = toggle.querySelector('[data-theme-icon="dark"]');
        if (lightIcon && darkIcon) {
            lightIcon.hidden = !isDark;
            darkIcon.hidden = isDark;
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
})();
