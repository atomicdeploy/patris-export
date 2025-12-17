const esbuild = require('esbuild');
const sass = require('sass');
const fs = require('fs');
const path = require('path');

const watch = process.argv.includes('--watch');

// Compile SCSS to CSS
function compileSass() {
  try {
    const result = sass.compile('src/styles.scss', {
      style: 'compressed',
      sourceMap: false
    });
    return result.css;
  } catch (error) {
    console.error('SCSS compilation error:', error);
    process.exit(1);
  }
}

// Build function
async function build() {
  console.log('🔨 Building frontend...');
  
  // Compile SCSS
  const css = compileSass();
  
  // Build JS with esbuild
  await esbuild.build({
    entryPoints: ['src/app.js'],
    bundle: true,
    minify: true,
    target: 'es2020',
    format: 'iife',
    outfile: 'dist/app.js',
    sourcemap: false,
  });
  
  // Read the built JS
  const js = fs.readFileSync('dist/app.js', 'utf8');
  
  // Read the HTML templates
  const viewerHtml = fs.readFileSync('src/viewer.html', 'utf8');
  const welcomeHtml = fs.readFileSync('src/welcome.html', 'utf8');
  
  // Inline everything into viewer.html
  const finalViewerHtml = viewerHtml
    .replace('<!-- STYLES -->', `<style>${css}</style>`)
    .replace('<!-- SCRIPTS -->', `<script>${js}</script>`);
  
  // Ensure dist directory exists
  if (!fs.existsSync('dist')) {
    fs.mkdirSync('dist');
  }
  
  // Write the final files
  fs.writeFileSync('dist/viewer.html', finalViewerHtml);
  fs.writeFileSync('dist/welcome.html', welcomeHtml);
  
  console.log('✅ Build complete: dist/viewer.html, dist/welcome.html');
}

// Run build
build().catch(err => {
  console.error('Build failed:', err);
  process.exit(1);
});

if (watch) {
  console.log('👀 Watching for changes...');
  // Simple file watcher
  const watchFiles = ['src/viewer.html', 'src/welcome.html', 'src/styles.scss', 'src/app.js'];
  watchFiles.forEach(file => {
    fs.watch(file, (eventType) => {
      if (eventType === 'change') {
        console.log(`\n📝 ${file} changed, rebuilding...`);
        build();
      }
    });
  });
}
