// Vite's `?raw` suffix, for fixtures a test reads as text. The app tsconfig
// carries no node types on purpose, so a test cannot reach the file with `fs`.
declare module '*?raw' {
  const contents: string;
  export default contents;
}
