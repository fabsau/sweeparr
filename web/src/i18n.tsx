import i18n from "i18next";
import LanguageDetector from "i18next-browser-languagedetector";
import { initReactI18next } from "react-i18next";
import { en } from "./locales/en";
import { de } from "./locales/de";
import { es } from "./locales/es";
import { fr } from "./locales/fr";
import { it } from "./locales/it";

export type Lang = "en" | "de" | "fr" | "es" | "it";

export const LANGUAGES: { code: Lang; label: string; native: string }[] = [
  { code: "de", label: "German", native: "Deutsch" },
  { code: "en", label: "English", native: "English" },
  { code: "es", label: "Spanish", native: "Español" },
  { code: "fr", label: "French", native: "Français" },
  { code: "it", label: "Italian", native: "Italiano" },
];

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: {
      en: { translation: en },
      de: { translation: de },
      fr: { translation: fr },
      es: { translation: es },
      it: { translation: it },
    },
    fallbackLng: "en",
    supportedLngs: ["en", "de", "fr", "es", "it"],
    load: "languageOnly",
    keySeparator: false,
    interpolation: {
      escapeValue: false,
    },
    detection: {
      order: ["localStorage", "navigator"],
      lookupLocalStorage: "sweeparr_lang",
      caches: ["localStorage"],
    },
  });

if (typeof document !== "undefined") {
  document.documentElement.lang = i18n.resolvedLanguage || i18n.language || "en";
  i18n.on("languageChanged", (lng) => {
    document.documentElement.lang = lng;
  });
}

export default i18n;
