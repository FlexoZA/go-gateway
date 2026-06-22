import type { Config } from "tailwindcss";

const config: Config = {
  content: [
    "./app/**/*.{ts,tsx}",
    "./components/**/*.{ts,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        ink: "#0b1120",
        panel: "#0f172a",
        edge: "#1e293b",
      },
    },
  },
  plugins: [],
};

export default config;
