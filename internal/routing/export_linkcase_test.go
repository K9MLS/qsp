package routing

// QSPLinkNamesForTest exposes the names a frame would be addressed to.
//
// **The names as configured, which is the whole point of the rule this
// supports.** Exported to the package's external tests rather than reaching
// into an unexported map, so the test asserts on what `route` builds targets
// from and not on a copy of it.
func QSPLinkNamesForTest(c *Core) []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.sortedQSPLinks()
}
