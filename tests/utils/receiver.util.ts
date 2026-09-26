import type http from 'node:http';

// The host name the backend uses to reach servers that specs run on this machine
// The Dockerized stack reaches the host through host.docker.internal, and a backend running directly on this machine sets RECEIVER_HOST=localhost
export const receiverHost = process.env.RECEIVER_HOST ?? 'host.docker.internal';

export function readBody(req: http.IncomingMessage) {
	return new Promise<string>((resolve, reject) => {
		let body = '';
		req.on('data', (chunk) => (body += chunk));
		req.on('end', () => resolve(body));
		req.on('error', reject);
	});
}
