(() => {
    const enhancedSelects = [];

    const closeLimitedSelect = (control, restoreFocus = false) => {
        if (!control.list || control.list.hidden) {
            return;
        }
        control.list.hidden = true;
        control.trigger.setAttribute("aria-expanded", "false");
        control.toggle.setAttribute("aria-expanded", "false");
        if (restoreFocus) {
            control.trigger.focus();
        }
    };

    document.querySelectorAll("[data-limited-select]").forEach((container) => {
        const select = container.querySelector("select");
        if (!select) {
            return;
        }

        const trigger = document.createElement("input");
        const toggle = document.createElement("button");
        const list = document.createElement("div");
        const originalName = select.name;
        const minimum = select.options[0]?.value || "0";
        const maximum = select.options[select.options.length - 1]?.value || "0";
        trigger.type = "number";
        trigger.name = originalName;
        trigger.min = minimum;
        trigger.max = maximum;
        trigger.step = "1";
        trigger.inputMode = "numeric";
        trigger.autocomplete = "off";
        trigger.value = select.value;
        trigger.id = select.id + "-trigger";
        trigger.className = "limited-select__trigger";
        trigger.setAttribute("role", "combobox");
        trigger.setAttribute("aria-haspopup", "listbox");
        trigger.setAttribute("aria-expanded", "false");
        trigger.setAttribute("aria-controls", select.id + "-options");

        toggle.type = "button";
        toggle.tabIndex = -1;
        toggle.className = "limited-select__toggle";
        toggle.setAttribute("aria-controls", select.id + "-options");
        toggle.setAttribute("aria-expanded", "false");
        toggle.setAttribute("aria-label", "Показать варианты");
        const labelID = container.dataset.labelId;
        if (labelID) {
            trigger.setAttribute("aria-labelledby", labelID);
            const label = document.getElementById(labelID);
            if (label) {
                label.htmlFor = trigger.id;
                toggle.setAttribute("aria-label", "Показать варианты: " + label.textContent.trim());
            }
        }
        if (select.getAttribute("aria-invalid") === "true") {
            trigger.setAttribute("aria-invalid", "true");
        }

        list.id = select.id + "-options";
        list.className = "limited-select__list";
        list.setAttribute("role", "listbox");
        list.setAttribute("aria-labelledby", labelID || trigger.id);
        list.hidden = true;

        const optionButtons = Array.from(select.options, (option) => {
            const button = document.createElement("button");
            button.type = "button";
            button.className = "limited-select__option";
            button.textContent = option.textContent;
            button.dataset.value = option.value;
            button.setAttribute("role", "option");
            button.setAttribute("aria-selected", String(option.selected));
            list.append(button);
            return button;
        });

        const control = {container, select, trigger, toggle, list, optionButtons};
        const updateSelection = (value) => {
            select.value = value;
            trigger.value = value;
            trigger.removeAttribute("aria-invalid");
            optionButtons.forEach((button) => {
                button.setAttribute("aria-selected", String(button.dataset.value === value));
            });
            trigger.dispatchEvent(new Event("input", {bubbles: true}));
        };
        const open = () => {
            enhancedSelects.forEach((other) => {
                if (other !== control) {
                    closeLimitedSelect(other);
                }
            });
            list.hidden = false;
            trigger.setAttribute("aria-expanded", "true");
            toggle.setAttribute("aria-expanded", "true");
            const selected = optionButtons.find((button) => button.getAttribute("aria-selected") === "true") || optionButtons[0];
            selected?.focus();
            selected?.scrollIntoView({block: "nearest"});
        };
        const moveFocus = (current, offset) => {
            const index = optionButtons.indexOf(current);
            const next = Math.min(optionButtons.length - 1, Math.max(0, index + offset));
            optionButtons[next]?.focus();
        };

        toggle.addEventListener("click", () => {
            if (list.hidden) {
                open();
            } else {
                closeLimitedSelect(control);
            }
        });
        trigger.addEventListener("keydown", (event) => {
            if (event.key === "ArrowDown" || event.key === "ArrowUp") {
                event.preventDefault();
                open();
            }
        });
        trigger.addEventListener("input", () => {
            const value = trigger.value;
            const selected = optionButtons.find((button) => button.dataset.value === value);
            select.value = selected ? value : "";
            optionButtons.forEach((button) => {
                button.setAttribute("aria-selected", String(button === selected));
            });
            if (selected) {
                trigger.removeAttribute("aria-invalid");
            }
        });
        trigger.addEventListener("blur", () => {
            if (!trigger.validity.valid) {
                trigger.setAttribute("aria-invalid", "true");
            }
        });
        optionButtons.forEach((button) => {
            button.addEventListener("click", () => {
                updateSelection(button.dataset.value);
                closeLimitedSelect(control, true);
            });
            button.addEventListener("keydown", (event) => {
                switch (event.key) {
                case "ArrowDown":
                    event.preventDefault();
                    moveFocus(button, 1);
                    break;
                case "ArrowUp":
                    event.preventDefault();
                    moveFocus(button, -1);
                    break;
                case "Home":
                    event.preventDefault();
                    optionButtons[0]?.focus();
                    break;
                case "End":
                    event.preventDefault();
                    optionButtons[optionButtons.length - 1]?.focus();
                    break;
                case "Escape":
                    event.preventDefault();
                    closeLimitedSelect(control, true);
                    break;
                }
            });
        });

        for (const attribute of ["data-rental-duration-days", "data-rental-duration-hours"]) {
            if (select.hasAttribute(attribute)) {
                select.removeAttribute(attribute);
                trigger.setAttribute(attribute, "");
            }
        }
        select.name = "";
        select.tabIndex = -1;
        select.setAttribute("aria-hidden", "true");
        container.append(trigger, toggle, list);
        container.classList.add("is-enhanced");
        container.addEventListener("focusout", () => {
            window.requestAnimationFrame(() => {
                if (!container.contains(document.activeElement)) {
                    closeLimitedSelect(control);
                }
            });
        });
        enhancedSelects.push(control);
    });

    document.addEventListener("pointerdown", (event) => {
        enhancedSelects.forEach((control) => {
            if (!control.container.contains(event.target)) {
                closeLimitedSelect(control);
            }
        });
    });

    const periodForm = document.querySelector("[data-rental-period-form]");
    if (periodForm) {
        const start = periodForm.querySelector("[data-rental-start]");
        const days = periodForm.querySelector("[data-rental-duration-days]");
        const hours = periodForm.querySelector("[data-rental-duration-hours]");
        const minutes = periodForm.querySelector("[data-rental-duration-minutes]");
        const durationTotal = periodForm.querySelector("[data-rental-duration-total]");
        const end = periodForm.querySelector("[data-rental-end]");

        const integerInRange = (input) => {
            if (input.value === "") {
                return 0;
            }
            const value = Number(input.value);
            const minimum = Number(input.min);
            const maximum = Number(input.max);
            if (!Number.isInteger(value) || value < minimum || value > maximum) {
                return null;
            }
            return value;
        };

        const updateEnd = () => {
            const match = start.value.match(/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/);
            const durationDays = integerInRange(days);
            const durationHours = integerInRange(hours);
            const durationMinutes = Number(minutes.value) || 0;
            if (durationDays === null || durationHours === null) {
                durationTotal.textContent = "Проверьте значения";
                end.textContent = "Укажите корректную продолжительность";
                return;
            }
            const totalMinutes = durationDays * 24 * 60 + durationHours * 60 + durationMinutes;

            const parts = [];
            if (durationDays > 0) {
                parts.push(durationDays + " сут.");
            }
            if (durationHours > 0) {
                parts.push(durationHours + " ч");
            }
            if (durationMinutes > 0 || parts.length === 0) {
                parts.push(durationMinutes + " мин");
            }
            durationTotal.textContent = parts.join(" ");

            if (!match || totalMinutes < 30) {
                end.textContent = "Укажите начало и продолжительность";
                return;
            }

            const value = new Date(Date.UTC(
                Number(match[1]), Number(match[2]) - 1, Number(match[3]),
                Number(match[4]), Number(match[5]) + totalMinutes,
            ));
            const twoDigits = (number) => String(number).padStart(2, "0");
            end.textContent = twoDigits(value.getUTCDate()) + "." +
                twoDigits(value.getUTCMonth() + 1) + "." + value.getUTCFullYear() + " " +
                twoDigits(value.getUTCHours()) + ":" + twoDigits(value.getUTCMinutes());
        };

        periodForm.addEventListener("input", updateEnd);
        updateEnd();
    }

    const form = document.querySelector("[data-rental-equipment-form]");
    if (!form) {
        return;
    }

    const slots = Number(form.dataset.slotCount);
    const itemCount = form.querySelector("[data-rental-item-count]");
    const total = form.querySelector("[data-rental-total]");
    const kindCounts = new Map();
    form.querySelectorAll("[data-rental-kind-count]").forEach((output) => {
        kindCounts.set(output.dataset.rentalKindCount, output);
    });

    const updateSummary = () => {
        let count = 0;
        let kopecks = 0;
        const counts = {sup_board: 0, paddle: 0, life_jacket: 0};

        form.querySelectorAll("[data-rental-quantity]").forEach((input) => {
            const maximum = Number(input.max);
            const quantity = Math.min(maximum, Math.max(0, Number(input.value) || 0));
            const rate = Number(input.dataset.hourlyRateKopecks);
            count += quantity;
            kopecks += quantity * rate * slots / 2;
            if (Object.prototype.hasOwnProperty.call(counts, input.dataset.equipmentKind)) {
                counts[input.dataset.equipmentKind] += quantity;
            }
        });

        itemCount.textContent = String(count);
        total.textContent = new Intl.NumberFormat("ru-RU").format(kopecks / 100) + " ₽";
        kindCounts.forEach((output, kind) => {
            output.textContent = String(counts[kind] || 0);
        });
    };

    form.addEventListener("input", updateSummary);
    updateSummary();
})();
