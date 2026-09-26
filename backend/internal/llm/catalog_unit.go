//go:build unit

package llm

// SwapCatalog installs a catalog whatever its age and returns a function that puts the previous one back
// It only exists in unit test builds, for the tests of packages that read the catalog
func SwapCatalog(c *ModelCatalog) (restore func()) {
	old := current.Swap(c)
	return func() { current.Store(old) }
}
