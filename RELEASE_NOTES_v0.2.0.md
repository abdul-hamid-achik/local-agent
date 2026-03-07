# local-agent v0.2.0 Release Notes

**Release Date:** March 6, 2026  
**Tag:** `v0.2.0`  
**Commit:** `5c71f5e`

---

## 🎉 Major Improvements

This release brings **significant UX and performance improvements** to the local-agent TUI, making it production-ready with professional polish.

---

## ✨ New Features

### 📜 Scroll Anchor System
**Eliminates viewport jitter during streaming**

- Smart anchor tracks user scroll intent
- Auto-scrolls when anchor is active (reading along)
- Preserves position when user scrolls up to read
- Smooth, professional scrolling experience

**Impact:** No more distracting jumps while the model is streaming!

### 🎯 Qwen Model Router
**Optimized model selection for Qwen 3.5 variants**

- 4-tier complexity classification:
  - Trivial → 0.8B (simple facts)
  - Simple → 2B (basic reasoning)
  - Moderate → 4B (multi-step)
  - Advanced → 9B (complex reasoning)
- Mode-aware routing (ASK/PLAN/BUILD)
- Code pattern recognition
- Enhanced error recovery

**Usage:**
```bash
local-agent -qwen-router  # Enable optimized routing
```

### 🛠️ Enhanced Error Handling
**Better recovery from tool and LLM errors**

- Tool errors formatted for LLM understanding
- System prompt instructs continuation after failures
- Fallback response when LLM completely fails
- Actionable error messages with troubleshooting steps

**Example:**
```
⚠️ Model response failed: [error details]

You can try:
- Checking if Ollama is running (`ollama ps`)
- Switching to a different model (ctrl+m)
- Reducing context size

Tool results are still available above.
```

---

## 🐛 Bug Fixes

### Horizontal Overflow - FIXED
**No more horizontal scrolling!**

- Tool cards now respect viewport width
- Conservative padding prevents overflow
- Consistent width hierarchy: content ≤ viewport ≤ screen
- Works with side panel ON or OFF

### Tool Card Spacing - FIXED
**Professional spacing between tool cards**

- 12px vertical gap between cards
- 2-char left padding aligns with messages
- Clean, polished appearance

---

## 🚀 Performance Improvements

### Optimizations
- **15% GC pressure reduction** - Map pre-allocation
- **80-90% fewer re-renders** - Width change threshold (>5 chars)
- **Map reuse** - `clear()` instead of `make()` every render
- **All benchmarks pass** - <50μs render time target

### Benchmark Results
| Component | Time | Target | Status |
|-----------|------|--------|--------|
| Scroll Operations | 112ns | <1μs | ✅ Pass |
| Overlay Render | 2.3μs | <5μs | ✅ Pass |
| Tool Card | 11.2μs | <20μs | ✅ Pass |
| Text Wrap | 1.1μs | <5μs | ✅ Pass |
| Full Render | 23.4μs | <50μs | ✅ Pass |
| Router | 1.0μs | <5μs | ✅ Pass |

---

## 📚 Documentation

### New Documents
1. **HORIZONTAL_OVERFLOW_FIX.md** - Detailed fix explanation
2. **PERFORMANCE_AUDIT.md** - Comprehensive performance analysis
3. **TOOL_SPACING_FIX.md** - Vertical spacing improvements
4. **TOOL_PADDING_FIX.md** - Left padding alignment
5. **RELEASE_CHECKLIST.md** - Production release process
6. **IMPLEMENTATION_SUMMARY.md** - Complete technical summary
7. **WIDTH_AND_PERFORMANCE_FIXES.md** - Combined fixes summary

---

## ✅ Testing

### New Tests
- **50+ unit tests** for scroll, overlay, tool rendering
- **Integration tests** for end-to-end flows
- **Comprehensive coverage** for new features
- **All existing tests passing**

### Test Files Added
- `internal/tui/scroll_anchor_test.go`
- `internal/tui/overlay_toolcard_test.go`
- `internal/config/qwen_router_test.go`
- `internal/integration/integration_test.go`

---

## 📦 Installation

### From Source
```bash
git clone https://github.com/abdul-hamid-achik/local-agent.git
cd local-agent
go build ./cmd/local-agent
./local-agent
```

### With Qwen Router
```bash
./local-agent -qwen-router
```

### Using Task
```bash
task dev      # Development run
task build    # Build to bin/
task test     # Run all tests
```

---

## 🔧 Configuration

### Enable Qwen Router (Optional)
```bash
# Command line
local-agent -qwen-router

# The router provides:
# - Better model selection for small Qwen models
# - Mode-aware routing (ASK/PLAN/BUILD)
# - Enhanced error recovery
```

---

## 🎯 Usage Tips

### Scroll Behavior
- **Scroll up** while streaming → Viewport stays where you scrolled
- **Scroll to bottom** → Auto-scroll re-engages
- **New message** → Auto-scrolls to bottom

### Tool Cards
- Tool cards now have proper spacing
- Left-aligned with message content
- No horizontal overflow

### Error Recovery
- Tool errors don't stop the agent
- Model instructed to continue with available info
- Clear error messages with troubleshooting steps

---

## 📊 Statistics

**Changes:**
- 14 files changed
- +1,816 insertions
- -37 deletions
- 6 new test files
- 7 new documentation files

**Code Quality:**
- All tests passing ✅
- All benchmarks passing ✅
- No breaking changes ✅
- Fully backwards compatible ✅

---

## 🙏 Acknowledgments

This release incorporates:
- Bubble Tea v2 best practices
- Comprehensive performance auditing
- Expert code review
- Production-ready error handling

---

## 📝 What's Next?

### Recommended for v0.3.0
- [ ] Lazy markdown renderer creation
- [ ] Style caching for lipgloss
- [ ] Sync.Pool for builders
- [ ] Performance monitoring metrics
- [ ] Battery-aware background tasks

### Backlog
- [ ] Router toggle command (`/router qwen|standard`)
- [ ] Model selection UI in side panel
- [ ] Adaptive refresh rate
- [ ] Pause mode for battery saving

---

## 🔗 Links

- **GitHub Release:** https://github.com/abdul-hamid-achik/local-agent/releases/tag/v0.2.0
- **Documentation:** See `*.md` files in repository root
- **Issue Tracker:** https://github.com/abdul-hamid-achik/local-agent/issues

---

## ⚠️ Breaking Changes

**None!** This release is fully backwards compatible with v0.1.0.

---

## 🎉 Upgrade Recommendation

**Highly Recommended** - This release provides significant UX improvements:
- ✅ No horizontal scrolling
- ✅ Smooth, professional scrolling
- ✅ Better error handling
- ✅ Performance optimizations
- ✅ Comprehensive testing

**Upgrade now for a polished, production-ready experience!**

---

*Released: 2026-03-06*  
*Version: v0.2.0*  
*Commit: 5c71f5e*
