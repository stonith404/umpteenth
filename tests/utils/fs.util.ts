import path from 'path';

export const tmpDir = pathFromRoot('.tmp');

// Resolves a path relative to the tests package, independent of the working directory
export function pathFromRoot(p: string): string {
	return path.resolve(path.dirname(new URL(import.meta.url).pathname), '..', p);
}
