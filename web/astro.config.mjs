import { defineConfig } from "astro/config";

export default defineConfig({
  base: "/sum",
  output: "static",
  site: "https://douglasjarquin.github.io",
  trailingSlash: "always"
});
