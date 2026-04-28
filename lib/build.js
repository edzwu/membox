const { exec } = require('child_process');

console.log('Building Hugo site...');
exec('hugo --minify', (error, stdout, stderr) => {
  if (stdout) process.stdout.write(stdout);
  if (stderr) process.stderr.write(stderr);

  if (error) {
    console.error('Build failed.');
    process.exit(error.code || 1);
  }

  console.log('Build finished.');
});
