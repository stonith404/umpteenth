#!/usr/bin/env python3
# Checks every link inside the built docs site, pages and #anchors alike, so a moved page or a renamed heading can't ship a dead link
# Usage: check_links.py [dist directory], which defaults to docs/dist, so run pnpm docs:build first

import sys
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import unquote, urljoin, urlparse

ROOT = Path(__file__).resolve().parents[4]


class Page(HTMLParser):
	# Collects the link targets and element ids of one HTML page
	def __init__(self):
		super().__init__()
		self.links = []
		self.ids = set()

	def handle_starttag(self, tag, attrs):
		attrs = dict(attrs)
		if attrs.get('id'):
			self.ids.add(attrs['id'])
		if tag == 'a' and attrs.get('href'):
			self.links.append(attrs['href'])


def url_of(dist, file):
	# The URL a built file answers at, with Starlight's trailing slash for index pages
	rel = file.relative_to(dist).as_posix()
	if rel == 'index.html':
		return '/'
	if rel.endswith('/index.html'):
		return '/' + rel[: -len('index.html')]
	return '/' + rel


def file_of(dist, path):
	# The built file that serves a URL path, or None when nothing does
	path = unquote(path).lstrip('/')
	for candidate in (dist / path, dist / path / 'index.html', dist / f'{path}.html'):
		if candidate.is_file():
			return candidate
	return None


def main(argv):
	dist = Path(argv[0]).resolve() if argv else ROOT / 'docs' / 'dist'
	if not dist.is_dir():
		print(f'{dist} does not exist, run pnpm docs:build first')
		return 1

	pages = {}
	for file in dist.rglob('*.html'):
		parser = Page()
		parser.feed(file.read_text(encoding='utf-8'))
		pages[file] = parser

	broken = 0
	checked = 0
	for file, page in sorted(pages.items()):
		base = url_of(dist, file)
		for href in page.links:
			parsed = urlparse(href)

			# External links and non-web schemes are out of scope
			if parsed.scheme or parsed.netloc or href.startswith(('mailto:', 'tel:', 'javascript:')):
				continue
			checked += 1

			target_url = urlparse(urljoin(base, href))
			target = file_of(dist, target_url.path) if target_url.path else file
			if target is None:
				print(f'{base}: broken link {href}')
				broken += 1
				continue

			# An anchor must match an id on the target page, which Starlight derives from the heading text
			fragment = unquote(target_url.fragment)
			if fragment and fragment != '_top' and target.suffix == '.html' and fragment not in pages[target].ids:
				print(f'{base}: missing anchor {href}')
				broken += 1

	print(f'\n{len(pages)} pages, {checked} internal links, {broken} broken')
	return 1 if broken else 0


if __name__ == '__main__':
	sys.exit(main(sys.argv[1:]))
