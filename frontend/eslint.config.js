import js from '@eslint/js';
import ts from 'typescript-eslint';
import svelte from 'eslint-plugin-svelte';
import prettier from 'eslint-config-prettier';
import globals from 'globals';
import { plugin as shadcn } from '@shadcn/lint';

/** @type {import('eslint').Linter.Config[]} */
export default [
	js.configs.recommended,
	...ts.configs.recommended,
	...svelte.configs['flat/recommended'],
	prettier,
	...svelte.configs['flat/prettier'],
	{
		languageOptions: {
			globals: globals.browser
		}
	},
	// Only the build config and the scripts run in Node, the app itself runs in the browser
	{
		files: ['*.js', '*.ts', 'scripts/**'],
		languageOptions: {
			globals: globals.node
		}
	},
	{
		files: ['**/*.svelte', '**/*.svelte.ts', '**/*.svelte.js'],
		languageOptions: {
			parserOptions: {
				parser: ts.parser
			}
		}
	},
	// Design system rules from @shadcn/lint, whose messages point at the variant or token to use instead
	// no-restyle and no-arbitrary-values only warn until their existing findings are worked off
	{
		files: ['**/*.svelte', '**/*.ts'],
		plugins: { shadcn },
		rules: {
			'shadcn/no-restyle': ['warn', { allow: ['layout'] }],
			'shadcn/no-arbitrary-values': 'warn',
			'shadcn/no-raw-colors': 'error',
			'shadcn/no-inline-styles': 'error',
			'shadcn/require-static-classes': 'error',
			// shadcn-svelte's styles tag component parts with cn-* marker classes that carry no CSS of their own
			'shadcn/no-unknown-classes': ['error', { allow: ['cn-*'] }]
		}
	},
	// The UI components own their appearance, so restyling them and structural values are fine inside them
	{
		files: ['src/lib/components/ui/**'],
		rules: {
			'shadcn/no-restyle': 'off',
			'shadcn/no-arbitrary-values': 'off',
			'shadcn/require-static-classes': 'off'
		}
	},
	// The data table takes its header and cell classes from the column definitions of each table
	{
		files: ['src/lib/components/data-table/data-table.svelte'],
		rules: {
			'shadcn/require-static-classes': 'off'
		}
	},
	{
		ignores: ['.svelte-kit/', 'dist/', 'src/lib/api/schema.d.ts']
	},
	{
		rules: {
			'@typescript-eslint/no-explicit-any': 'off',
			// Umpteenth does not support deployments under a SvelteKit base path
			'svelte/no-navigation-without-resolve': 'off'
		}
	}
];
