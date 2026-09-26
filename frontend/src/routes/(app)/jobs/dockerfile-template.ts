// A starting point for a job's Dockerfile that installs nothing yet, shared by the new-job review and the Environment tab
export function dockerfileTemplate(baseImage: string) {
	return `FROM ${baseImage}\n\n# Install extra tools here, e.g.\n# RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg && rm -rf /var/lib/apt/lists/*\n`;
}
