// Recreates the placeholder go:embed needs in backend/frontend/dist, since adapter-static empties that directory on every build
// Builds with BUILD_OUTPUT_PATH, e.g. inside the Docker image, write elsewhere and have nothing to restore
import { existsSync, writeFileSync } from 'node:fs';

const dist = new URL('../../backend/frontend/dist/', import.meta.url);
if (!process.env.BUILD_OUTPUT_PATH && existsSync(dist)) {
	writeFileSync(new URL('.gitkeep', dist), '');
}
