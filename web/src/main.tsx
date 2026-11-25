import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./i18n";
import { applyTheme, getStoredAccentColor, getStoredThemeMode } from "./utils/theme";
import { App } from "./App";
import "./styles.css";

applyTheme(getStoredThemeMode(), getStoredAccentColor());

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>
);
