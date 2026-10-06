// Copyright 2013 Julien Schmidt. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be found
// at https://github.com/julienschmidt/httprouter/blob/master/LICENSE

package gin

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin/internal/bytesconv"
)

// Param is a single URL parameter, consisting of a key and a value.
type Param struct {
	Key   string
	Value string
}

// Params is a Param-slice, as returned by the router.
// The slice is ordered, the first URL parameter is also the first slice value.
// It is therefore safe to read values by the index.
type Params []Param

// Get returns the value of the first Param which key matches the given name and a boolean true.
// If no matching Param is found, an empty string is returned and a boolean false .
func (ps Params) Get(name string) (string, bool) {
	for _, entry := range ps {
		if entry.Key == name {
			return entry.Value, true
		}
	}
	return "", false
}

// ByName returns the value of the first Param which key matches the given name.
// If no matching Param is found, an empty string is returned.
func (ps Params) ByName(name string) (va string) {
	va, _ = ps.Get(name)
	return
}

type methodTree struct {
	method string
	root   *node
}

type methodTrees []methodTree

func (trees methodTrees) get(method string) *node {
	for _, tree := range trees {
		if tree.method == method {
			return tree.root
		}
	}
	return nil
}

func longestCommonPrefix(a, b string) int {
	i := 0
	max_ := min(len(a), len(b))
	for i < max_ && a[i] == b[i] {
		i++
	}
	return i
}

// addChild will add a child node, keeping wildcardChild at the end
func (n *node) addChild(child *node) {
	if n.wildChild && len(n.children) > 0 {
		wildcardChild := n.children[len(n.children)-1]
		n.children = append(n.children[:len(n.children)-1], child, wildcardChild)
	} else {
		n.children = append(n.children, child)
	}
}

func countParams(path string) uint16 {
	colons := strings.Count(path, ":")
	stars := strings.Count(path, "*")
	return safeUint16(colons + stars)
}

func countSections(path string) uint16 {
	return safeUint16(strings.Count(path, "/"))
}

type nodeType uint8

const (
	static nodeType = iota
	root
	param
	catchAll
)

type node struct {
	path      string
	indices   string
	wildChild bool
	nType     nodeType
	priority  uint32
	children  []*node // child nodes, at most 1 :param style node at the end of the array
	handlers  HandlersChain
	fullPath  string
}

// incrementChildPrio increments the priority of the given child and reorders it if necessary.
func (n *node) incrementChildPrio(pos int) int {
	cs := n.children
	cs[pos].priority++
	prio := cs[pos].priority

	// Adjust position (move to front)
	newPos := pos
	for ; newPos > 0 && cs[newPos-1].priority < prio; newPos-- {
		// Swap node positions
		cs[newPos-1], cs[newPos] = cs[newPos], cs[newPos-1]
	}

	// Build new index char string
	if newPos != pos {
		n.indices = n.indices[:newPos] + // Unchanged prefix, might be empty
			n.indices[pos:pos+1] + // The index char we move
			n.indices[newPos:pos] + n.indices[pos+1:] // Rest without char at 'pos'
	}

	return newPos
}

// addRoute adds a node with the given handle to the path.
// Not concurrency-safe!
func (n *node) addRoute(path string, handlers HandlersChain) {
	fullPath := path
	n.priority++

	// Empty tree
	if len(n.path) == 0 && len(n.children) == 0 {
		n.insertChild(path, fullPath, handlers)
		n.nType = root
		return
	}

	parentFullPathIndex := 0

walk:
	for {
		// Find the longest common prefix.
		// This also implies that the common prefix contains no ':' or '*'
		// since the existing key can't contain those chars.
		i := longestCommonPrefix(path, n.path)

		// Split edge
		if i < len(n.path) {
			child := node{
				path:      n.path[i:],
				wildChild: n.wildChild,
				nType:     static,
				indices:   n.indices,
				children:  n.children,
				handlers:  n.handlers,
				priority:  n.priority - 1,
				fullPath:  n.fullPath,
			}

			n.children = []*node{&child}
			// []byte for proper unicode char conversion, see #65
			n.indices = bytesconv.BytesToString([]byte{n.path[i]})
			n.path = path[:i]
			n.handlers = nil
			n.wildChild = false
			n.fullPath = fullPath[:parentFullPathIndex+i]
		}

		// Make new node a child of this node
		if i < len(path) {
			path = path[i:]
			c := path[0]

			// '/' after param
			if n.nType == param && c == '/' && len(n.children) == 1 {
				parentFullPathIndex += len(n.path)
				n = n.children[0]
				n.priority++
				continue walk
			}

			// Check if a child with the next path byte exists
			for i, max_ := 0, len(n.indices); i < max_; i++ {
				if c == n.indices[i] {
					parentFullPathIndex += len(n.path)
					i = n.incrementChildPrio(i)
					n = n.children[i]
					continue walk
				}
			}

			// Otherwise insert it
			if c != ':' && c != '*' && n.nType != catchAll {
				// []byte for proper unicode char conversion, see #65
				n.indices += bytesconv.BytesToString([]byte{c})
				child := &node{
					fullPath: fullPath,
				}
				n.addChild(child)
				n.incrementChildPrio(len(n.indices) - 1)
				n = child
			} else if n.wildChild {
				// inserting a wildcard node, need to check if it conflicts with the existing wildcard
				n = n.children[len(n.children)-1]
				n.priority++

				// Check if the wildcard matches
				if len(path) >= len(n.path) && n.path == path[:len(n.path)] &&
					// Adding a child to a catchAll is not possible
					n.nType != catchAll &&
					// Check for longer wildcard, e.g. :name and :names
					(len(n.path) >= len(path) || path[len(n.path)] == '/') {
					continue walk
				}

				// Wildcard conflict
				pathSeg := path
				if n.nType != catchAll {
					pathSeg, _, _ = strings.Cut(pathSeg, "/")
				}
				prefix := fullPath[:strings.Index(fullPath, pathSeg)] + n.path
				panic("'" + pathSeg +
					"' in new path '" + fullPath +
					"' conflicts with existing wildcard '" + n.path +
					"' in existing prefix '" + prefix +
					"'")
			}

			n.insertChild(path, fullPath, handlers)
			return
		}

		// Otherwise add handle to current node
		if n.handlers != nil {
			panic("handlers are already registered for path '" + fullPath + "'")
		}
		n.handlers = handlers
		n.fullPath = fullPath
		return
	}
}

// Search for a wildcard segment and check the name for invalid characters.
// Returns -1 as index, if no wildcard was found.
func findWildcard(path string) (wildcard string, i int, valid bool) {
	// Find start
	escapeColon := false
	for start, c := range []byte(path) {
		if escapeColon {
			escapeColon = false
			if c == ':' {
				continue
			}
			panic("invalid escape string in path '" + path + "'")
		}
		if c == '\\' {
			escapeColon = true
			continue
		}
		// A wildcard starts with ':' (param) or '*' (catch-all)
		if c != ':' && c != '*' {
			continue
		}

		// Find end and check for invalid characters
		valid = true
		for end, c := range []byte(path[start+1:]) {
			switch c {
			case '/':
				return path[start : start+1+end], start, valid
			case ':', '*':
				valid = false
			}
		}
		return path[start:], start, valid
	}
	return "", -1, false
}

func (n *node) insertChild(path string, fullPath string, handlers HandlersChain) {
	for {
		// Find prefix until first wildcard
		wildcard, i, valid := findWildcard(path)
		if i < 0 { // No wildcard found
			break
		}

		// The wildcard name must only contain one ':' or '*' character
		if !valid {
			panic("only one wildcard per path segment is allowed, has: '" +
				wildcard + "' in path '" + fullPath + "'")
		}

		// check if the wildcard has a name
		if len(wildcard) < 2 {
			panic("wildcards must be named with a non-empty name in path '" + fullPath + "'")
		}

		if wildcard[0] == ':' { // param
			if i > 0 {
				// Insert prefix before the current wildcard
				n.path = path[:i]
				path = path[i:]
			}

			child := &node{
				nType:    param,
				path:     wildcard,
				fullPath: fullPath,
			}
			n.addChild(child)
			n.wildChild = true
			n = child
			n.priority++

			// if the path doesn't end with the wildcard, then there
			// will be another subpath starting with '/'
			if len(wildcard) < len(path) {
				path = path[len(wildcard):]

				child := &node{
					priority: 1,
					fullPath: fullPath,
				}
				n.addChild(child)
				n = child
				continue
			}

			// Otherwise we're done. Insert the handle in the new leaf
			n.handlers = handlers
			return
		}

		// catchAll
		if i+len(wildcard) != len(path) {
			panic("catch-all routes are only allowed at the end of the path in path '" + fullPath + "'")
		}

		if len(n.path) > 0 && n.path[len(n.path)-1] == '/' {
			pathSeg := ""
			if len(n.children) != 0 {
				pathSeg, _, _ = strings.Cut(n.children[0].path, "/")
			}
			panic("catch-all wildcard '" + path +
				"' in new path '" + fullPath +
				"' conflicts with existing path segment '" + pathSeg +
				"' in existing prefix '" + n.path + pathSeg +
				"'")
		}

		// currently fixed width 1 for '/'
		i--
		if i < 0 || path[i] != '/' {
			panic("no / before catch-all in path '" + fullPath + "'")
		}

		n.path = path[:i]

		// First node: catchAll node with empty path
		child := &node{
			wildChild: true,
			nType:     catchAll,
			fullPath:  fullPath,
		}

		n.addChild(child)
		n.indices = "/"
		n = child
		n.priority++

		// second node: node holding the variable
		child = &node{
			path:     path[i:],
			nType:    catchAll,
			handlers: handlers,
			priority: 1,
			fullPath: fullPath,
		}
		n.children = []*node{child}

		return
	}

	// If no wildcard was found, simply insert the path and handle
	n.path = path
	n.handlers = handlers
	n.fullPath = fullPath
}

// nodeValue holds return values of (*Node).getValue method
type nodeValue struct {
	handlers HandlersChain
	params   *Params
	tsr      bool
	fullPath string
}

type skippedNode struct {
	path        string
	node        *node
	paramsCount int16
}

// Returns the handle registered with the given path (key). The values of
// wildcards are saved to a map.
// If no handle can be found, a TSR (trailing slash redirect) recommendation is
// made if a handle exists with an extra (without the) trailing slash for the
// given path.
func (n *node) getValue(path string, params *Params, skippedNodes *[]skippedNode, unescape bool) (value nodeValue) {
	var globalParamsCount int16

	// Reset the skipped-nodes stack on entry. getValue is called once per
	// method tree (e.g. in the HandleMethodNotAllowed loop) reusing the same
	// pooled Context stack, and the walk below grows it via a raw reslice that
	// cannot exceed engine.maxSections. Without this reset the residue from a
	// previous tree leaks into the next call and can panic with "slice bounds
	// out of range" once the accumulated length passes the capacity.
	*skippedNodes = (*skippedNodes)[:0]

walk: // Outer loop for walking the tree
	for {
		prefix := n.path
		if len(path) > len(prefix) {
			if path[:len(prefix)] == prefix {
				path = path[len(prefix):]

				// Try all the non-wildcard children first by matching the indices
				idxc := path[0]
				for i, c := range []byte(n.indices) {
					if c == idxc {
						//  strings.HasPrefix(n.children[len(n.children)-1].path, ":") == n.wildChild
						if n.wildChild {
							index := len(*skippedNodes)
							*skippedNodes = (*skippedNodes)[:index+1]
							(*skippedNodes)[index] = skippedNode{
								path: prefix + path,
								node: &node{
									path:      n.path,
									wildChild: n.wildChild,
									nType:     n.nType,
									priority:  n.priority,
									children:  n.children,
									handlers:  n.handlers,
									fullPath:  n.fullPath,
								},
								paramsCount: globalParamsCount,
							}
						}

						n = n.children[i]
						continue walk
					}
				}

				if !n.wildChild {
					// If the path at the end of the loop is not equal to '/' and the current node has no child nodes
					// the current node needs to roll back to last valid skippedNode
					if path != "/" {
						for length := len(*skippedNodes); length > 0; length-- {
							skippedNode := (*skippedNodes)[length-1]
							*skippedNodes = (*skippedNodes)[:length-1]
							if strings.HasSuffix(skippedNode.path, path) {
								path = skippedNode.path
								n = skippedNode.node
								if value.params != nil {
									*value.params = (*value.params)[:skippedNode.paramsCount]
								}
								globalParamsCount = skippedNode.paramsCount
								continue walk
							}
						}
					}

					// Nothing found.
					// We can recommend to redirect to the same URL without a
					// trailing slash if a leaf exists for that path.
					value.tsr = path == "/" && n.handlers != nil
					return value
				}

				// Handle wildcard child, which is always at the end of the array
				n = n.children[len(n.children)-1]
				globalParamsCount++

				switch n.nType {
				case param:
					// fix truncate the parameter
					// tree_test.go  line: 204

					// Find param end (either '/' or path end)
					end := 0
					for end < len(path) && path[end] != '/' {
						end++
					}

					// Save param value
					if params != nil {
						// Preallocate capacity if necessary
						if cap(*params) < int(globalParamsCount) {
							newParams := make(Params, len(*params), globalParamsCount)
							copy(newParams, *params)
							*params = newParams
						}

						if value.params == nil {
							value.params = params
						}
						// Expand slice within preallocated capacity
						i := len(*value.params)
						*value.params = (*value.params)[:i+1]
						val := path[:end]
						if unescape {
							if v, err := url.QueryUnescape(val); err == nil {
								val = v
							}
						}
						(*value.params)[i] = Param{
							Key:   n.path[1:],
							Value: val,
						}
					}

					// we need to go deeper!
					if end < len(path) {
						if len(n.children) > 0 {
							path = path[end:]
							n = n.children[0]
							continue walk
						}

						// ... but we can't
						value.tsr = len(path) == end+1
						return value
					}

					if value.handlers = n.handlers; value.handlers != nil {
						value.fullPath = n.fullPath
						return value
					}
					if len(n.children) == 1 {
						// No handle found. Check if a handle for this path + a
						// trailing slash exists for TSR recommendation
						n = n.children[0]
						value.tsr = (n.path == "/" && n.handlers != nil) || (n.path == "" && n.indices == "/")
					}
					return value

				case catchAll:
					// Save param value
					if params != nil {
						// Preallocate capacity if necessary
						if cap(*params) < int(globalParamsCount) {
							newParams := make(Params, len(*params), globalParamsCount)
							copy(newParams, *params)
							*params = newParams
						}

						if value.params == nil {
							value.params = params
						}
						// Expand slice within preallocated capacity
						i := len(*value.params)
						*value.params = (*value.params)[:i+1]
						val := path
						if unescape {
							if v, err := url.QueryUnescape(path); err == nil {
								val = v
							}
						}
						(*value.params)[i] = Param{
							Key:   n.path[2:],
							Value: val,
						}
					}

					value.handlers = n.handlers
					value.fullPath = n.fullPath
					return value

				default:
					panic("invalid node type")
				}
			}
		}

		if path == prefix {
			// If the current path does not equal '/' and the node does not have a registered handle and the most recently matched node has a child node
			// the current node needs to roll back to last valid skippedNode
			if n.handlers == nil && path != "/" {
				for length := len(*skippedNodes); length > 0; length-- {
					skippedNode := (*skippedNodes)[length-1]
					*skippedNodes = (*skippedNodes)[:length-1]
					if strings.HasSuffix(skippedNode.path, path) {
						path = skippedNode.path
						n = skippedNode.node
						if value.params != nil {
							*value.params = (*value.params)[:skippedNode.paramsCount]
						}
						globalParamsCount = skippedNode.paramsCount
						continue walk
					}
				}
				//	n = latestNode.children[len(latestNode.children)-1]
			}
			// We should have reached the node containing the handle.
			// Check if this node has a handle registered.
			if value.handlers = n.handlers; value.handlers != nil {
				value.fullPath = n.fullPath
				return value
			}

			// If there is no handle for this route, but this route has a
			// wildcard child, there must be a handle for this path with an
			// additional trailing slash
			if path == "/" && n.wildChild && n.nType != root {
				value.tsr = true
				return value
			}

			if path == "/" && n.nType == static {
				value.tsr = true
				return value
			}

			// No handle found. Check if a handle for this path + a
			// trailing slash exists for trailing slash recommendation
			for i, c := range []byte(n.indices) {
				if c == '/' {
					n = n.children[i]
					value.tsr = (len(n.path) == 1 && n.handlers != nil) ||
						(n.nType == catchAll && n.children[0].handlers != nil)
					return value
				}
			}

			return value
		}

		// Nothing found. We can recommend to redirect to the same URL with an
		// extra trailing slash if a leaf exists for that path
		value.tsr = path == "/" ||
			(len(prefix) == len(path)+1 && prefix[len(path)] == '/' &&
				path == prefix[:len(prefix)-1] && n.handlers != nil)

		// roll back to last valid skippedNode
		if !value.tsr && path != "/" {
			for length := len(*skippedNodes); length > 0; length-- {
				skippedNode := (*skippedNodes)[length-1]
				*skippedNodes = (*skippedNodes)[:length-1]
				if strings.HasSuffix(skippedNode.path, path) {
					path = skippedNode.path
					n = skippedNode.node
					if value.params != nil {
						*value.params = (*value.params)[:skippedNode.paramsCount]
					}
					globalParamsCount = skippedNode.paramsCount
					continue walk
				}
			}
		}

		return value
	}
}

// Makes a case-insensitive lookup of the given path and tries to find a handler.
// It can optionally also fix trailing slashes.
// It returns the case-corrected path and a bool indicating whether the lookup
// was successful.
func (n *node) findCaseInsensitivePath(path string, fixTrailingSlash bool) ([]byte, bool) {
	const stackBufSize = 128

	buf := make([]byte, 0, max(stackBufSize, len(path)+1))

	ciPath := n.findCaseInsensitivePathRec(
		path,
		buf,       // Preallocate enough memory for new path
		[4]byte{}, // Empty rune buffer
		fixTrailingSlash,
	)

	return ciPath, ciPath != nil
}

// shiftNRuneBytes shifts bytes in the array n positions to the left.
func shiftNRuneBytes(rb [4]byte, n int) [4]byte {
	switch n {
	case 0:
		return rb
	case 1:
		return [4]byte{rb[1], rb[2], rb[3], 0}
	case 2:
		return [4]byte{rb[2], rb[3]}
	case 3:
		return [4]byte{rb[3]}
	default:
		return [4]byte{}
	}
}

// findCaseInsensitivePathRec recursively performs a case-insensitive path lookup.
func (n *node) findCaseInsensitivePathRec(path string, ciPath []byte, rb [4]byte, fixTrailingSlash bool) []byte {
	npLen := len(n.path)

walk: // Outer loop for walking the tree
	for len(path) >= npLen && (npLen == 0 || strings.EqualFold(path[1:npLen], n.path[1:])) {
		// Add common prefix to result
		oldPath := path
		path = path[npLen:]
		ciPath = append(ciPath, n.path...)

		if len(path) == 0 {
			// We should have reached the node containing the handle.
			// Check if this node has a handle registered.
			if n.handlers != nil {
				return ciPath
			}

			// No handle found.
			// Try to fix the path by adding a trailing slash
			if fixTrailingSlash {
				for i, c := range []byte(n.indices) {
					if c == '/' {
						n = n.children[i]
						if (len(n.path) == 1 && n.handlers != nil) ||
							(n.nType == catchAll && n.children[0].handlers != nil) {
							return append(ciPath, '/')
						}
						return nil
					}
				}
			}
			return nil
		}

		// If this node does not have a wildcard (param or catchAll) child,
		// we can just look up the next child node and continue to walk down
		// the tree
		if !n.wildChild {
			// Skip rune bytes already processed
			rb = shiftNRuneBytes(rb, npLen)

			if rb[0] != 0 {
				// Old rune not finished
				idxc := rb[0]
				for i, c := range []byte(n.indices) {
					if c == idxc {
						// continue with child node
						n = n.children[i]
						npLen = len(n.path)
						continue walk
					}
				}
			} else {
				// Process a new rune
				var rv rune

				// Find rune start.
				// Runes are up to 4 byte long,
				// -4 would definitely be another rune.
				var off int
				for max_ := min(npLen, 3); off < max_; off++ {
					if i := npLen - off; utf8.RuneStart(oldPath[i]) {
						// read rune from cached path
						rv, _ = utf8.DecodeRuneInString(oldPath[i:])
						break
					}
				}

				// Calculate lowercase bytes of current rune
				lo := unicode.ToLower(rv)
				utf8.EncodeRune(rb[:], lo)

				// Skip already processed bytes
				rb = shiftNRuneBytes(rb, off)

				idxc := rb[0]
				for i, c := range []byte(n.indices) {
					// Lowercase matches
					if c == idxc {
						// must use a recursive approach since both the
						// uppercase byte and the lowercase byte might exist
						// as an index
						if out := n.children[i].findCaseInsensitivePathRec(
							path, ciPath, rb, fixTrailingSlash,
						); out != nil {
							return out
						}
						break
					}
				}

				// If we found no match, the same for the uppercase rune,
				// if it differs
				if up := unicode.ToUpper(rv); up != lo {
					utf8.EncodeRune(rb[:], up)
					rb = shiftNRuneBytes(rb, off)

					idxc := rb[0]
					for i, c := range []byte(n.indices) {
						// Uppercase matches
						if c == idxc {
							// Continue with child node
							n = n.children[i]
							npLen = len(n.path)
							continue walk
						}
					}
				}
			}

			// Nothing found. We can recommend to redirect to the same URL
			// without a trailing slash if a leaf exists for that path
			if fixTrailingSlash && path == "/" && n.handlers != nil {
				return ciPath
			}
			return nil
		}

		// When wildChild is true, try static children first (via indices)
		// before falling back to the wildcard child. This ensures that
		// case-insensitive lookups prefer static routes over param routes
		// (e.g., /PREFIX/XXX should resolve to /prefix/xxx, not match :id).
		if len(n.indices) > 0 {
			rb = shiftNRuneBytes(rb, npLen)

			if rb[0] != 0 {
				idxc := rb[0]
				for i, c := range []byte(n.indices) {
					if c == idxc {
						if out := n.children[i].findCaseInsensitivePathRec(
							path, ciPath, rb, fixTrailingSlash,
						); out != nil {
							return out
						}
						break
					}
				}
			} else {
				var rv rune
				var off int
				for max_ := min(npLen, 3); off < max_; off++ {
					if i := npLen - off; utf8.RuneStart(oldPath[i]) {
						rv, _ = utf8.DecodeRuneInString(oldPath[i:])
						break
					}
				}

				lo := unicode.ToLower(rv)
				utf8.EncodeRune(rb[:], lo)
				rb = shiftNRuneBytes(rb, off)

				idxc := rb[0]
				for i, c := range []byte(n.indices) {
					if c == idxc {
						if out := n.children[i].findCaseInsensitivePathRec(
							path, ciPath, rb, fixTrailingSlash,
						); out != nil {
							return out
						}
						break
					}
				}

				if up := unicode.ToUpper(rv); up != lo {
					utf8.EncodeRune(rb[:], up)
					rb = shiftNRuneBytes(rb, off)

					idxc := rb[0]
					for i, c := range []byte(n.indices) {
						if c == idxc {
							if out := n.children[i].findCaseInsensitivePathRec(
								path, ciPath, rb, fixTrailingSlash,
							); out != nil {
								return out
							}
							break
						}
					}
				}
			}
		}

		// Fall back to wildcard child, which is always at the end of the array
		n = n.children[len(n.children)-1]
		switch n.nType {
		case param:
			// Find param end (either '/' or path end)
			end := 0
			for end < len(path) && path[end] != '/' {
				end++
			}

			// Add param value to case insensitive path
			ciPath = append(ciPath, path[:end]...)

			// We need to go deeper!
			if end < len(path) {
				if len(n.children) > 0 {
					// Continue with child node
					n = n.children[0]
					npLen = len(n.path)
					path = path[end:]
					continue
				}

				// ... but we can't
				if fixTrailingSlash && len(path) == end+1 {
					return ciPath
				}
				return nil
			}

			if n.handlers != nil {
				return ciPath
			}

			if fixTrailingSlash && len(n.children) == 1 {
				// No handle found. Check if a handle for this path + a
				// trailing slash exists
				n = n.children[0]
				if n.path == "/" && n.handlers != nil {
					return append(ciPath, '/')
				}
			}

			return nil

		case catchAll:
			return append(ciPath, path...)

		default:
			panic("invalid node type")
		}
	}

	// Nothing found.
	// Try to fix the path by adding / removing a trailing slash
	if fixTrailingSlash {
		if path == "/" {
			return ciPath
		}
		if len(path)+1 == npLen && n.path[len(path)] == '/' &&
			strings.EqualFold(path[1:], n.path[1:len(path)]) && n.handlers != nil {
			return append(ciPath, n.path...)
		}
	}
	return nil
}



	assert.Equal(t, "file", s.FileData.Protocol)
	assert.Equal(t, "/foo", s.FileData.Path)
	assert.Equal(t, "happiness", s.FileData.Name)
}

func TestMappingCustomPointerStructTypeWithFormTag(t *testing.T) {
	var s struct {
		FileData *customUnmarshalParamType `form:"data"`
	}
	err := mappingByPtr(&s, formSource{"data": {`file:/foo:happiness`}}, "form")
	require.NoError(t, err)

	assert.Equal(t, "file", s.FileData.Protocol)
	assert.Equal(t, "/foo", s.FileData.Path)
	assert.Equal(t, "happiness", s.FileData.Name)
}

func TestMappingCustomPointerStructTypeWithURITag(t *testing.T) {
	var s struct {
		FileData *customUnmarshalParamType `uri:"data"`
	}
	err := mappingByPtr(&s, formSource{"data": {`file:/foo:happiness`}}, "uri")
	require.NoError(t, err)

	assert.Equal(t, "file", s.FileData.Protocol)
	assert.Equal(t, "/foo", s.FileData.Path)
	assert.Equal(t, "happiness", s.FileData.Name)
}

type customPath []string

func (p *customPath) UnmarshalParam(param string) error {
	elems := strings.Split(param, "/")
	n := len(elems)
	if n < 2 {
		return errors.New("invalid format")
	}

	*p = elems
	return nil
}

func TestMappingCustomSliceUri(t *testing.T) {
	var s struct {
		FileData customPath `uri:"path"`
	}
	err := mappingByPtr(&s, formSource{"path": {`bar/foo`}}, "uri")
	require.NoError(t, err)

	assert.Equal(t, "bar", s.FileData[0])
	assert.Equal(t, "foo", s.FileData[1])
}

func TestMappingCustomSliceForm(t *testing.T) {
	var s struct {
		FileData customPath `form:"path"`
	}
	err := mappingByPtr(&s, formSource{"path": {`bar/foo`}}, "form")
	require.NoError(t, err)

	assert.Equal(t, "bar", s.FileData[0])
	assert.Equal(t, "foo", s.FileData[1])
}

func TestMappingCustomSliceStopsWhenError(t *testing.T) {
	var s struct {
		FileData customPath `form:"path"`
	}
	err := mappingByPtr(&s, formSource{"path": {"invalid"}}, "form")
	require.ErrorContains(t, err, "invalid format")
	require.Empty(t, s.FileData)
}

func TestMappingCustomSliceOfSliceUri(t *testing.T) {
	var s struct {
		FileData []customPath `uri:"path" collection_format:"csv"`
	}
	err := mappingByPtr(&s, formSource{"path": {"bar/foo,bar/foo/spam"}}, "uri")
	require.NoError(t, err)
	assert.Equal(t, []customPath{{"bar", "foo"}, {"bar", "foo", "spam"}}, s.FileData)
}

func TestMappingCustomSliceOfSliceForm(t *testing.T) {
	var s struct {
		FileData []customPath `form:"path" collection_format:"csv"`
	}
	err := mappingByPtr(&s, formSource{"path": {"bar/foo,bar/foo/spam"}}, "form")
	require.NoError(t, err)
	assert.Equal(t, []customPath{{"bar", "foo"}, {"bar", "foo", "spam"}}, s.FileData)
}

type objectID [12]byte

func (o *objectID) UnmarshalParam(param string) error {
	oid, err := convertTo(param)
	if err != nil {
		return err
	}

	*o = oid
	return nil
}

func convertTo(s string) (objectID, error) {
	var nilObjectID objectID
	if len(s) != 24 {
		return nilObjectID, errors.New("invalid format")
	}

	var oid [12]byte
	_, err := hex.Decode(oid[:], []byte(s))
	if err != nil {
		return nilObjectID, err
	}

	return oid, nil
}

func TestMappingCustomArrayUri(t *testing.T) {
	var s struct {
		FileData objectID `uri:"id"`
	}
	val := `664a062ac74a8ad104e0e80f`
	err := mappingByPtr(&s, formSource{"id": {val}}, "uri")
	require.NoError(t, err)

	expected, _ := convertTo(val)
	assert.Equal(t, expected, s.FileData)
}

func TestMappingCustomArrayForm(t *testing.T) {
	var s struct {
		FileData objectID `form:"id"`
	}
	val := `664a062ac74a8ad104e0e80f`
	err := mappingByPtr(&s, formSource{"id": {val}}, "form")
	require.NoError(t, err)

	expected, _ := convertTo(val)
	assert.Equal(t, expected, s.FileData)
}

func TestMappingCustomArrayOfArrayUri(t *testing.T) {
	id1, _ := convertTo(`664a062ac74a8ad104e0e80e`)
	id2, _ := convertTo(`664a062ac74a8ad104e0e80f`)

	var s struct {
		FileData []objectID `uri:"ids" collection_format:"csv"`
	}
	err := mappingByPtr(&s, formSource{"ids": {`664a062ac74a8ad104e0e80e,664a062ac74a8ad104e0e80f`}}, "uri")
	require.NoError(t, err)
	assert.Equal(t, []objectID{id1, id2}, s.FileData)
}

func TestMappingCustomArrayOfArrayForm(t *testing.T) {
	id1, _ := convertTo(`664a062ac74a8ad104e0e80e`)
	id2, _ := convertTo(`664a062ac74a8ad104e0e80f`)

	var s struct {
		FileData []objectID `form:"ids" collection_format:"csv"`
	}
	err := mappingByPtr(&s, formSource{"ids": {`664a062ac74a8ad104e0e80e,664a062ac74a8ad104e0e80f`}}, "form")
	require.NoError(t, err)
	assert.Equal(t, []objectID{id1, id2}, s.FileData)
}

// ====  TextUnmarshaler tests START ====

type customUnmarshalTextHex int

func (f *customUnmarshalTextHex) UnmarshalText(text []byte) error {
	v, err := strconv.ParseInt(string(text), 16, 64)
	if err != nil {
		return err
	}
	*f = customUnmarshalTextHex(v)
	return nil
}

// verify type implements TextUnmarshaler
var _ encoding.TextUnmarshaler = (*customUnmarshalTextHex)(nil)

func TestMappingCustomUnmarshalTextHexUri(t *testing.T) {
	var s struct {
		Field customUnmarshalTextHex `uri:"field,parser=encoding.TextUnmarshaler"`
	}
	err := mappingByPtr(&s, formSource{"field": {`f5`}}, "uri")
	require.NoError(t, err)
	assert.EqualValues(t, 245, s.Field)
}

func TestMappingCustomUnmarshalTextHexForm(t *testing.T) {
	var s struct {
		Field customUnmarshalTextHex `form:"field,parser=encoding.TextUnmarshaler"`
	}
	err := mappingByPtr(&s, formSource{"field": {`f5`}}, "form")
	require.NoError(t, err)
	assert.EqualValues(t, 245, s.Field)
}

func TestMappingCustomUnmarshalTextHexDefault(t *testing.T) {
	var s struct {
		Field customUnmarshalTextHex `form:"field,default=f5,parser=encoding.TextUnmarshaler"`
	}
	err := mappingByPtr(&s, formSource{"field1": {}}, "form")
	require.NoError(t, err)
	assert.EqualValues(t, 0xf5, s.Field)
}

type customUnmarshalTextType struct {
	Protocol string
	Path     string
	Name     string
}

func (f *customUnmarshalTextType) UnmarshalText(text []byte) error {
	parts := strings.Split(string(text), ":")
	if len(parts) != 3 {
		return errors.New("invalid format")
	}
	f.Protocol = parts[0]
	f.Path = parts[1]
	f.Name = parts[2]
	return nil
}

var _ encoding.TextUnmarshaler = (*customUnmarshalTextType)(nil)

func TestMappingCustomStructTypeUnmarshalTextForm(t *testing.T) {
	var s struct {
		FileData customUnmarshalTextType `form:"data,parser=encoding.TextUnmarshaler"`
	}
	err := mappingByPtr(&s, formSource{"data": {`file:/foo:happiness`}}, "form")
	require.NoError(t, err)

	assert.Equal(t, "file", s.FileData.Protocol)
	assert.Equal(t, "/foo", s.FileData.Path)
	assert.Equal(t, "happiness", s.FileData.Name)
}

func TestMappingCustomStructTypeUnmarshalTextUri(t *testing.T) {
	var s struct {
		FileData customUnmarshalTextType `uri:"data,parser=encoding.TextUnmarshaler"`
	}
	err := mappingByPtr(&s, formSource{"data": {`file:/foo:happiness`}}, "uri")
	require.NoError(t, err)

	assert.Equal(t, "file", s.FileData.Protocol)
	assert.Equal(t, "/foo", s.FileData.Path)
	assert.Equal(t, "happiness", s.FileData.Name)
}

func TestMappingCustomPointerStructTypeUnmarshalTextForm(t *testing.T) {
	var s struct {
		FileData *customUnmarshalTextType `form:"data,parser=encoding.TextUnmarshaler"`
	}
	err := mappingByPtr(&s, formSource{"data": {`file:/foo:happiness`}}, "form")
	require.NoError(t, err)

	assert.Equal(t, "file", s.FileData.Protocol)
	assert.Equal(t, "/foo", s.FileData.Path)
	assert.Equal(t, "happiness", s.FileData.Name)
}

func TestMappingCustomPointerStructTypeUnmarshalTextUri(t *testing.T) {
	var s struct {
		FileData *customUnmarshalTextType `uri:"data,parser=encoding.TextUnmarshaler"`
	}
	err := mappingByPtr(&s, formSource{"data": {`file:/foo:happiness`}}, "uri")
	require.NoError(t, err)

	assert.Equal(t, "file", s.FileData.Protocol)
	assert.Equal(t, "/foo", s.FileData.Path)
	assert.Equal(t, "happiness", s.FileData.Name)
}

type customPathUnmarshalText []string

func (p *customPathUnmarshalText) UnmarshalText(text []byte) error {
	elems := strings.Split(string(text), "/")
	n := len(elems)
	if n < 2 {
		return errors.New("invalid format")
	}

	*p = elems
	return nil
}

var _ encoding.TextUnmarshaler = (*customPathUnmarshalText)(nil)

func TestMappingCustomSliceUnmarshalTextUri(t *testing.T) {
	var s struct {
		FileData customPathUnmarshalText `uri:"path,parser=encoding.TextUnmarshaler"`
	}
	err := mappingByPtr(&s, formSource{"path": {`bar/foo`}}, "uri")
	require.NoError(t, err)

	assert.Equal(t, "bar", s.FileData[0])
	assert.Equal(t, "foo", s.FileData[1])
}

func TestMappingCustomSliceUnmarshalTextForm(t *testing.T) {
	var s struct {
		FileData customPathUnmarshalText `form:"path,parser=encoding.TextUnmarshaler"`
	}
	err := mappingByPtr(&s, formSource{"path": {`bar/foo`}}, "form")
	require.NoError(t, err)

	assert.Equal(t, "bar", s.FileData[0])
	assert.Equal(t, "foo", s.FileData[1])
}

func TestMappingCustomSliceUnmarshalTextStopsWhenError(t *testing.T) {
	var s struct {
		FileData customPathUnmarshalText `form:"path,parser=encoding.TextUnmarshaler"`
	}
	err := mappingByPtr(&s, formSource{"path": {"invalid"}}, "form")
	require.ErrorContains(t, err, "invalid format")
	require.Empty(t, s.FileData)
}

func TestMappingCustomSliceOfSliceUnmarshalTextUri(t *testing.T) {
	var s struct {
		FileData []customPathUnmarshalText `uri:"path,parser=encoding.TextUnmarshaler" collection_format:"csv"`
	}
	err := mappingByPtr(&s, formSource{"path": {"bar/foo,bar/foo/spam"}}, "uri")
	require.NoError(t, err)
	assert.Equal(t, []customPathUnmarshalText{{"bar", "foo"}, {"bar", "foo", "spam"}}, s.FileData)
}

func TestMappingCustomSliceOfSliceUnmarshalTextForm(t *testing.T) {
	var s struct {
		FileData []customPathUnmarshalText `form:"path,parser=encoding.TextUnmarshaler" collection_format:"csv"`
	}
	err := mappingByPtr(&s, formSource{"path": {"bar/foo,bar/foo/spam"}}, "form")
	require.NoError(t, err)
	assert.Equal(t, []customPathUnmarshalText{{"bar", "foo"}, {"bar", "foo", "spam"}}, s.FileData)
}

func TestMappingCustomSliceOfSliceUnmarshalTextDefault(t *testing.T) {
	var s struct {
		FileData []customPathUnmarshalText `form:"path,default=bar/foo;bar/foo/spam,parser=encoding.TextUnmarshaler" collection_format:"csv"`
	}
	err := mappingByPtr(&s, formSource{"path": {}}, "form")
	require.NoError(t, err)
	assert.Equal(t, []customPathUnmarshalText{{"bar", "foo"}, {"bar", "foo", "spam"}}, s.FileData)
}

type objectIDUnmarshalText [12]byte

func (o *objectIDUnmarshalText) UnmarshalText(text []byte) error {
	oid, err := convertToOidUnmarshalText(string(text))
	if err != nil {
		return err
	}

	*o = oid
	return nil
}

func convertToOidUnmarshalText(s string) (objectIDUnmarshalText, error) {
	oid, err := convertTo(s)
	return objectIDUnmarshalText(oid), err
}

var _ encoding.TextUnmarshaler = (*objectIDUnmarshalText)(nil)

func TestMappingCustomArrayUnmarshalTextUri(t *testing.T) {
	var s struct {
		FileData objectIDUnmarshalText `uri:"id,parser=encoding.TextUnmarshaler"`
	}
	val := `664a062ac74a8ad104e0e80f`
	err := mappingByPtr(&s, formSource{"id": {val}}, "uri")
	require.NoError(t, err)

	expected, _ := convertToOidUnmarshalText(val)
	assert.Equal(t, expected, s.FileData)
}

func TestMappingCustomArrayUnmarshalTextForm(t *testing.T) {
	var s struct {
		FileData objectIDUnmarshalText `form:"id,parser=encoding.TextUnmarshaler"`
	}
	val := `664a062ac74a8ad104e0e80f`
	err := mappingByPtr(&s, formSource{"id": {val}}, "form")
	require.NoError(t, err)

	expected, _ := convertToOidUnmarshalText(val)
	assert.Equal(t, expected, s.FileData)
}

func TestMappingCustomArrayOfArrayUnmarshalTextUri(t *testing.T) {
	id1, _ := convertToOidUnmarshalText(`664a062ac74a8ad104e0e80e`)
	id2, _ := convertToOidUnmarshalText(`664a062ac74a8ad104e0e80f`)

	var s struct {
		FileData []objectIDUnmarshalText `uri:"ids,parser=encoding.TextUnmarshaler" collection_format:"csv"`
	}
	err := mappingByPtr(&s, formSource{"ids": {`664a062ac74a8ad104e0e80e,664a062ac74a8ad104e0e80f`}}, "uri")
	require.NoError(t, err)
	assert.Equal(t, []objectIDUnmarshalText{id1, id2}, s.FileData)
}

func TestMappingCustomArrayOfArrayUnmarshalTextForm(t *testing.T) {
	id1, _ := convertToOidUnmarshalText(`664a062ac74a8ad104e0e80e`)
	id2, _ := convertToOidUnmarshalText(`664a062ac74a8ad104e0e80f`)

	var s struct {
		FileData []objectIDUnmarshalText `form:"ids,parser=encoding.TextUnmarshaler" collection_format:"csv"`
	}
	err := mappingByPtr(&s, formSource{"ids": {`664a062ac74a8ad104e0e80e,664a062ac74a8ad104e0e80f`}}, "form")
	require.NoError(t, err)
	assert.Equal(t, []objectIDUnmarshalText{id1, id2}, s.FileData)
}

func TestMappingCustomArrayOfArrayUnmarshalTextDefault(t *testing.T) {
	id1, _ := convertToOidUnmarshalText(`664a062ac74a8ad104e0e80e`)
	id2, _ := convertToOidUnmarshalText(`664a062ac74a8ad104e0e80f`)

	var s struct {
		FileData []objectIDUnmarshalText `form:"ids,default=664a062ac74a8ad104e0e80e;664a062ac74a8ad104e0e80f,parser=encoding.TextUnmarshaler" collection_format:"csv"`
	}
	err := mappingByPtr(&s, formSource{"ids": {}}, "form")
	require.NoError(t, err)
	assert.Equal(t, []objectIDUnmarshalText{id1, id2}, s.FileData)
}

// If someone specifies parser=TextUnmarshaler and it's not defined for the type, gin should revert to using its default
// binding logic.
func TestMappingUsingBindUnmarshalerAndTextUnmarshalerWhenOnlyBindUnmarshalerDefined(t *testing.T) {
	var s struct {
		Hex                customUnmarshalParamHex `form:"hex"`
		HexByUnmarshalText customUnmarshalParamHex `form:"hex2,parser=encoding.TextUnmarshaler"`
	}
	err := mappingByPtr(&s, formSource{
		"hex":  {`f5`},
		"hex2": {`f5`},
	}, "form")
	require.NoError(t, err)

	assert.EqualValues(t, 0xf5, s.Hex)
	assert.EqualValues(t, 0xf5, s.HexByUnmarshalText) // reverts to BindUnmarshaler binding
}

// If someone does not specify parser=TextUnmarshaler even when it's defined for the type, gin should ignore the
// UnmarshalText logic and continue using its default binding logic. (This ensures gin does not break backwards
// compatibility)
func TestMappingUsingBindUnmarshalerAndTextUnmarshalerWhenOnlyTextUnmarshalerDefined(t *testing.T) {
	var s struct {
		Hex                customUnmarshalTextHex `form:"hex"`
		HexByUnmarshalText customUnmarshalTextHex `form:"hex2,parser=encoding.TextUnmarshaler"`
	}
	err := mappingByPtr(&s, formSource{
		"hex":  {`11`},
		"hex2": {`11`},
	}, "form")
	require.NoError(t, err)

	assert.EqualValues(t, 11, s.Hex)                  // this is using default int binding, not our custom hex binding. 0x11 should be 17 in decimal
	assert.EqualValues(t, 0x11, s.HexByUnmarshalText) // correct expected value for normal hex binding
}

type customHexUnmarshalParamAndUnmarshalText int

func (f *customHexUnmarshalParamAndUnmarshalText) UnmarshalParam(param string) error {
	return errors.New("should not be called in unit test if parser tag present")
}

func (f *customHexUnmarshalParamAndUnmarshalText) UnmarshalText(text []byte) error {
	v, err := strconv.ParseInt(string(text), 16, 64)
	if err != nil {
		return err
	}
	*f = customHexUnmarshalParamAndUnmarshalText(v)
	return nil
}

// If a type has both UnmarshalParam and UnmarshalText methods defined, but the parser tag is set to TextUnmarshaler,
// then only the UnmarshalText method should be invoked.
func TestMappingUsingTextUnmarshalerWhenBindUnmarshalerAlsoDefined(t *testing.T) {
	var s struct {
		Hex customHexUnmarshalParamAndUnmarshalText `form:"hex,parser=encoding.TextUnmarshaler"`
	}
	err := mappingByPtr(&s, formSource{
		"hex": {`f5`},
	}, "form")
	require.NoError(t, err)

	assert.EqualValues(t, 0xf5, s.Hex)
}

// ====  TextUnmarshaler tests END ====

func TestMappingEmptyValues(t *testing.T) {
	t.Run("slice with default", func(t *testing.T) {
		var s struct {
			Slice []int `form:"slice,default=5"`
		}

		// field not present
		err := mappingByPtr(&s, formSource{}, "form")
		require.NoError(t, err)
		assert.Equal(t, []int{5}, s.Slice)

		// field present but empty
		err = mappingByPtr(&s, formSource{"slice": {}}, "form")
		require.NoError(t, err)
		assert.Equal(t, []int{5}, s.Slice)

		// field present with values
		err = mappingByPtr(&s, formSource{"slice": {"1", "2", "3"}}, "form")
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2, 3}, s.Slice)
	})

	t.Run("array with default", func(t *testing.T) {
		var s struct {
			Array [1]int `form:"array,default=5"`
		}

		// field not present
		err := mappingByPtr(&s, formSource{}, "form")
		require.NoError(t, err)
		assert.Equal(t, [1]int{5}, s.Array)

		// field present but empty
		err = mappingByPtr(&s, formSource{"array": {}}, "form")
		require.NoError(t, err)
		assert.Equal(t, [1]int{5}, s.Array)
	})

	t.Run("slice without default", func(t *testing.T) {
		var s struct {
			Slice []int `form:"slice"`
		}

		// field present but empty
		err := mappingByPtr(&s, formSource{"slice": {}}, "form")
		require.NoError(t, err)
		assert.Equal(t, []int(nil), s.Slice)
	})

	t.Run("array without default", func(t *testing.T) {
		var s struct {
			Array [1]int `form:"array"`
		}

		// field present but empty
		err := mappingByPtr(&s, formSource{"array": {}}, "form")
		require.NoError(t, err)
		assert.Equal(t, [1]int{0}, s.Array)
	})

	t.Run("slice with collection format", func(t *testing.T) {
		var s struct {
			SliceMulti []int `form:"slice_multi,default=1;2;3" collection_format:"multi"`
			SliceCsv   []int `form:"slice_csv,default=1;2;3" collection_format:"csv"`
		}

		// field not present
		err := mappingByPtr(&s, formSource{}, "form")
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2, 3}, s.SliceMulti)
		assert.Equal(t, []int{1, 2, 3}, s.SliceCsv)

		// field present but empty
		err = mappingByPtr(&s, formSource{"slice_multi": {}, "slice_csv": {}}, "form")
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2, 3}, s.SliceMulti)
		assert.Equal(t, []int{1, 2, 3}, s.SliceCsv)
	})
}
