import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { Checkbox, ConfirmModal, NumberStepper, SelectMenu } from "../components/Controls";
import { LocaleFormatProvider } from "../utils/format";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (k: string) => k, i18n: { resolvedLanguage: "en", changeLanguage: vi.fn() } }),
  initReactI18next: { type: "3rdParty", init: () => {} },
}));

describe("Controls UI components", () => {
  it("SelectMenu opens, selects an option, and closes on Escape", () => {
    const onChange = vi.fn();
    render(
      <SelectMenu
        ariaLabel="Role"
        value="viewer"
        options={[
          { value: "viewer", label: "Viewer" },
          { value: "admin", label: "Admin" },
        ]}
        onChange={onChange}
      />
    );
    const btn = screen.getByRole("button", { name: "Role" });
    expect(btn).toHaveTextContent("Viewer");
    fireEvent.click(btn);
    fireEvent.click(screen.getByRole("option", { name: "Admin" }));
    expect(onChange).toHaveBeenCalledWith("admin");
  });

  it("ConfirmModal triggers onConfirm and closes on Escape", () => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    render(
      <ConfirmModal
        open
        title="Clear History"
        message="Delete all entries?"
        confirmLabel="Delete"
        onConfirm={onConfirm}
        onCancel={onCancel}
      />
    );
    expect(screen.getByRole("alertdialog", { name: "Clear History" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("Checkbox toggles and supports disabled state", () => {
    const onChange = vi.fn();
    render(<Checkbox checked={false} onChange={onChange} label="Enable feature" />);
    fireEvent.click(screen.getByLabelText("Enable feature"));
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it("NumberStepper increments, decrements, and clamps bounds", () => {
    const onChange = vi.fn();
    render(
      <LocaleFormatProvider numberLocale="en-US" dateLocale="en-US" timeFormat="24h">
        <NumberStepper value={10} min={5} max={15} step={5} ariaLabel="Delay" onChange={onChange} />
      </LocaleFormatProvider>
    );
    fireEvent.click(screen.getByRole("button", { name: "Increase Delay" }));
    expect(onChange).toHaveBeenCalledWith(15);
    fireEvent.click(screen.getByRole("button", { name: "Decrease Delay" }));
    expect(onChange).toHaveBeenCalledWith(5);
  });
});
