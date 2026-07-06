import { readFileSync } from 'node:fs';

const files = ['../src/app/App.jsx'];
const seen = new Map();

for (const file of files) {
  const source = readFileSync(new URL(file, import.meta.url), 'utf8');
  const lineStarts = [0];
  for (let i = 0; i < source.length; i += 1) {
    if (source[i] === '\n') lineStarts.push(i + 1);
  }
  const lineOf = (index) => {
    let low = 0;
    let high = lineStarts.length - 1;
    while (low <= high) {
      const mid = Math.floor((low + high) / 2);
      if (lineStarts[mid] <= index) low = mid + 1;
      else high = mid - 1;
    }
    return high + 1;
  };

  for (const match of source.matchAll(/\b(?:async\s+)?function\s+([A-Za-z_$][\w$]*)\s*\(/g)) {
    const name = match[1];
    const locations = seen.get(name) || [];
    locations.push(`${file.replace('../', '')}:${lineOf(match.index)}`);
    seen.set(name, locations);
  }
}

const duplicates = [...seen.entries()].filter(([, locations]) => locations.length > 1);
if (duplicates.length) {
  for (const [name, locations] of duplicates) {
    console.error(`duplicate function name "${name}": ${locations.join(', ')}`);
  }
  process.exit(1);
}

console.log(`function name check passed: ${seen.size} declarations`);
