package tui

// list is a scrolling cursor over n rows.
type list struct {
	cursor, offset, n int
}

func (l *list) set(n int) {
	l.n = n
	if l.cursor >= n {
		l.cursor = max(0, n-1)
	}
}

func (l *list) move(d, height int) {
	if l.n == 0 {
		return
	}
	l.cursor = max(0, min(l.n-1, l.cursor+d))
	l.scroll(height)
}

func (l *list) jump(i, height int) {
	if l.n == 0 {
		return
	}
	l.cursor = max(0, min(l.n-1, i))
	l.scroll(height)
}

// scroll keeps the cursor inside the visible window of height rows.
func (l *list) scroll(height int) {
	if height < 1 {
		return
	}
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+height {
		l.offset = l.cursor - height + 1
	}
	l.offset = max(0, min(l.offset, l.n-height))
}

// window returns the row range [from, to) to draw.
func (l *list) window(height int) (int, int) {
	l.scroll(height)
	return l.offset, min(l.n, l.offset+height)
}
