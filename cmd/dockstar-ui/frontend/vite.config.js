import { writeFileSync } from 'node:fs';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [{
    name: 'preserve-embed-directory',
    closeBundle() {
      writeFileSync('dist/.gitkeep', '');
    },
  }],
});
