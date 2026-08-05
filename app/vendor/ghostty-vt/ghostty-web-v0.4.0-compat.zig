//! Compatibility ABI for ghostty-web 0.4.0.
//!
//! ghostty-web predates libghostty-vt's current C API. The browser wrapper
//! expects one opaque handle to own both the terminal and render state and it
//! reads a packed 16-byte cell array. Keep that ABI at this boundary while
//! delegating all VT processing and storage to the current terminal core.

const std = @import("std");
const lib = @import("../lib.zig");
const terminal_c = @import("terminal.zig");
const renderpkg = @import("../render.zig");
const colorpkg = @import("../color.zig");
const Style = @import("../style.zig").Style;
const modespkg = @import("../modes.zig");

const Allocator = std.mem.Allocator;

const TerminalWrapper = struct {
    alloc: Allocator,
    terminal: terminal_c.Terminal,
    render_state: renderpkg.RenderState = .empty,
    response_buffer: std.ArrayList(u8) = .empty,
};

pub const Cell = extern struct {
    codepoint: u32,
    fg_r: u8,
    fg_g: u8,
    fg_b: u8,
    bg_r: u8,
    bg_g: u8,
    bg_b: u8,
    flags: u8,
    width: u8,
    hyperlink_id: u16,
    grapheme_len: u8 = 0,
    _pad: u8 = 0,
};

pub const Config = extern struct {
    scrollback_limit: u32,
    fg_color: u32,
    bg_color: u32,
    cursor_color: u32,
    palette: [16]u32,
};

pub const Dirty = enum(u8) {
    none = 0,
    partial = 1,
    full = 2,
};

fn rgb(value: u32) colorpkg.RGB.C {
    return .{
        .r = @truncate(value >> 16),
        .g = @truncate(value >> 8),
        .b = @truncate(value),
    };
}

fn writePty(
    _: terminal_c.Terminal,
    userdata: ?*anyopaque,
    ptr: [*]const u8,
    len: usize,
) callconv(lib.calling_conv) void {
    const wrapper: *TerminalWrapper = @ptrCast(@alignCast(userdata orelse return));
    wrapper.response_buffer.appendSlice(wrapper.alloc, ptr[0..len]) catch {};
}

pub fn new(cols: c_int, rows: c_int) callconv(lib.calling_conv) ?*anyopaque {
    return newWithConfig(cols, rows, null);
}

pub fn newWithConfig(
    cols: c_int,
    rows: c_int,
    config: ?*const Config,
) callconv(lib.calling_conv) ?*anyopaque {
    if (cols <= 0 or rows <= 0) return null;

    const alloc = lib.alloc.default(null);
    const wrapper = alloc.create(TerminalWrapper) catch return null;
    wrapper.* = .{ .alloc = alloc, .terminal = null };
    errdefer alloc.destroy(wrapper);

    const max_scrollback: usize = if (config) |cfg|
        if (cfg.scrollback_limit == 0) std.math.maxInt(usize) else cfg.scrollback_limit
    else
        10_000;
    if (terminal_c.new(null, &wrapper.terminal, .{
        .cols = @intCast(cols),
        .rows = @intCast(rows),
        .max_scrollback = max_scrollback,
    }) != .success) return null;
    errdefer terminal_c.free(wrapper.terminal);

    // The legacy API guaranteed usable colors even without a config.
    var foreground = rgb(0xCCCCCC);
    var background = rgb(0x000000);
    if (config) |cfg| {
        if (cfg.fg_color != 0) foreground = rgb(cfg.fg_color);
        if (cfg.bg_color != 0) background = rgb(cfg.bg_color);
    }
    _ = terminal_c.set(wrapper.terminal, .color_foreground, @ptrCast(&foreground));
    _ = terminal_c.set(wrapper.terminal, .color_background, @ptrCast(&background));

    if (config) |cfg| {
        if (cfg.cursor_color != 0) {
            var cursor = rgb(cfg.cursor_color);
            _ = terminal_c.set(wrapper.terminal, .color_cursor, @ptrCast(&cursor));
        }

        var palette = colorpkg.paletteCval(&colorpkg.default);
        var changed = false;
        for (cfg.palette, 0..) |value, i| {
            if (value == 0) continue;
            palette[i] = rgb(value);
            changed = true;
        }
        if (changed) _ = terminal_c.set(wrapper.terminal, .color_palette, @ptrCast(&palette));
    }

    _ = terminal_c.set(wrapper.terminal, .userdata, @ptrCast(wrapper));
    _ = terminal_c.set(wrapper.terminal, .write_pty, @ptrCast(&writePty));

    // Preserve ghostty-web's defaults. The app also enables mode 2027 itself,
    // but setting it here keeps standalone users of the wrapper compatible.
    const terminal = terminal_c.zigTerminal(wrapper.terminal).?;
    terminal.modes.set(.linefeed, false);
    terminal.modes.set(.grapheme_cluster, true);
    terminal.flags.shell_redraws_prompt = .true;

    return @ptrCast(wrapper);
}

pub fn free(ptr: ?*anyopaque) callconv(lib.calling_conv) void {
    const wrapper: *TerminalWrapper = @ptrCast(@alignCast(ptr orelse return));
    const alloc = wrapper.alloc;
    wrapper.render_state.deinit(alloc);
    wrapper.response_buffer.deinit(alloc);
    terminal_c.free(wrapper.terminal);
    alloc.destroy(wrapper);
}

pub fn resize(ptr: ?*anyopaque, cols: c_int, rows: c_int) callconv(lib.calling_conv) void {
    const wrapper: *TerminalWrapper = @ptrCast(@alignCast(ptr orelse return));
    if (cols <= 0 or rows <= 0) return;
    _ = terminal_c.resize(wrapper.terminal, @intCast(cols), @intCast(rows), 0, 0);
}

pub fn write(ptr: ?*anyopaque, data: [*]const u8, len: usize) callconv(lib.calling_conv) void {
    const wrapper: *TerminalWrapper = @ptrCast(@alignCast(ptr orelse return));
    terminal_c.vt_write(wrapper.terminal, data, len);
}

pub fn renderStateUpdate(ptr: ?*anyopaque) callconv(lib.calling_conv) Dirty {
    const wrapper: *TerminalWrapper = @ptrCast(@alignCast(ptr orelse return .full));
    const terminal = terminal_c.zigTerminal(wrapper.terminal) orelse return .full;
    wrapper.render_state.update(wrapper.alloc, terminal) catch return .full;
    return switch (wrapper.render_state.dirty) {
        .false => .none,
        .partial => .partial,
        .full => .full,
    };
}

pub fn renderStateGetCols(ptr: ?*anyopaque) callconv(lib.calling_conv) c_int {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return 0));
    return @intCast(wrapper.render_state.cols);
}

pub fn renderStateGetRows(ptr: ?*anyopaque) callconv(lib.calling_conv) c_int {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return 0));
    return @intCast(wrapper.render_state.rows);
}

pub fn renderStateGetCursorX(ptr: ?*anyopaque) callconv(lib.calling_conv) c_int {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return 0));
    return if (wrapper.render_state.cursor.viewport) |cursor| @intCast(cursor.x) else 0;
}

pub fn renderStateGetCursorY(ptr: ?*anyopaque) callconv(lib.calling_conv) c_int {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return 0));
    return if (wrapper.render_state.cursor.viewport) |cursor| @intCast(cursor.y) else 0;
}

pub fn renderStateGetCursorVisible(ptr: ?*anyopaque) callconv(lib.calling_conv) bool {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return false));
    return wrapper.render_state.cursor.visible and wrapper.render_state.cursor.viewport != null;
}

pub fn renderStateGetBgColor(ptr: ?*anyopaque) callconv(lib.calling_conv) u32 {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return 0));
    const value = wrapper.render_state.colors.background;
    return (@as(u32, value.r) << 16) | (@as(u32, value.g) << 8) | value.b;
}

pub fn renderStateGetFgColor(ptr: ?*anyopaque) callconv(lib.calling_conv) u32 {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return 0xCCCCCC));
    const value = wrapper.render_state.colors.foreground;
    return (@as(u32, value.r) << 16) | (@as(u32, value.g) << 8) | value.b;
}

pub fn renderStateIsRowDirty(ptr: ?*anyopaque, y: c_int) callconv(lib.calling_conv) bool {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return true));
    if (wrapper.render_state.dirty == .full) return true;
    if (wrapper.render_state.dirty == .false or y < 0) return false;
    const index: usize = @intCast(y);
    if (index >= wrapper.render_state.row_data.len) return false;
    return wrapper.render_state.row_data.items(.dirty)[index];
}

pub fn renderStateMarkClean(ptr: ?*anyopaque) callconv(lib.calling_conv) void {
    const wrapper: *TerminalWrapper = @ptrCast(@alignCast(ptr orelse return));
    wrapper.render_state.dirty = .false;
    @memset(wrapper.render_state.row_data.items(.dirty), false);
}

fn blankCell(wrapper: *const TerminalWrapper) Cell {
    return .{
        .codepoint = 0,
        .fg_r = wrapper.render_state.colors.foreground.r,
        .fg_g = wrapper.render_state.colors.foreground.g,
        .fg_b = wrapper.render_state.colors.foreground.b,
        .bg_r = wrapper.render_state.colors.background.r,
        .bg_g = wrapper.render_state.colors.background.g,
        .bg_b = wrapper.render_state.colors.background.b,
        .flags = 0,
        .width = 1,
        .hyperlink_id = 0,
    };
}

fn fillCell(wrapper: *const TerminalWrapper, pin: anytype, x: usize) Cell {
    const cells = pin.cells(.all);
    if (x >= cells.len) return blankCell(wrapper);

    const cell = &cells[x];
    const page = pin.node.page();
    const style: Style = if (cell.hasStyling())
        page.styles.get(page.memory, cell.style_id).*
    else
        .{};
    const foreground: colorpkg.RGB = switch (style.fg_color) {
        .none => wrapper.render_state.colors.foreground,
        .palette => |index| wrapper.render_state.colors.palette[index],
        .rgb => |value| value,
    };
    const background = style.bg(cell, &wrapper.render_state.colors.palette) orelse
        wrapper.render_state.colors.background;

    var flags: u8 = 0;
    if (style.flags.bold) flags |= 1 << 0;
    if (style.flags.italic) flags |= 1 << 1;
    if (style.flags.underline != .none) flags |= 1 << 2;
    if (style.flags.strikethrough) flags |= 1 << 3;
    if (style.flags.inverse) flags |= 1 << 4;
    if (style.flags.invisible) flags |= 1 << 5;
    if (style.flags.blink) flags |= 1 << 6;
    if (style.flags.faint) flags |= 1 << 7;

    const grapheme_len: u8 = if (cell.hasGrapheme())
        if (page.lookupGrapheme(cell)) |cps| @intCast(@min(cps.len, 255)) else 0
    else
        0;

    return .{
        .codepoint = cell.codepoint(),
        .fg_r = foreground.r,
        .fg_g = foreground.g,
        .fg_b = foreground.b,
        .bg_r = background.r,
        .bg_g = background.g,
        .bg_b = background.b,
        .flags = flags,
        .width = switch (cell.wide) {
            .narrow => 1,
            .wide => 2,
            .spacer_tail, .spacer_head => 0,
        },
        .hyperlink_id = if (cell.hyperlink) 1 else 0,
        .grapheme_len = grapheme_len,
    };
}

pub fn renderStateGetViewport(
    ptr: ?*anyopaque,
    out: [*]Cell,
    buffer_size: usize,
) callconv(lib.calling_conv) c_int {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return -1));
    const terminal = terminal_c.zigTerminal(wrapper.terminal) orelse return -1;
    const rows: usize = wrapper.render_state.rows;
    const cols: usize = wrapper.render_state.cols;
    const total = rows * cols;
    if (buffer_size < total) return -1;

    var index: usize = 0;
    for (0..rows) |y| {
        const pin = terminal.screens.active.pages.pin(.{ .active = .{ .y = @intCast(y) } });
        for (0..cols) |x| {
            out[index] = if (pin) |row_pin| fillCell(wrapper, row_pin, x) else blankCell(wrapper);
            index += 1;
        }
    }
    return @intCast(total);
}

fn getGrapheme(
    wrapper: *const TerminalWrapper,
    comptime location: enum { active, history },
    y: c_int,
    x: c_int,
    out: [*]u32,
    buffer_size: usize,
) c_int {
    if (y < 0 or x < 0 or buffer_size == 0) return -1;
    const terminal = terminal_c.zigTerminal(wrapper.terminal) orelse return -1;
    const point = switch (location) {
        .active => @import("../point.zig").Point{ .active = .{ .x = @intCast(x), .y = @intCast(y) } },
        .history => @import("../point.zig").Point{ .history = .{ .x = @intCast(x), .y = @intCast(y) } },
    };
    const pin = terminal.screens.active.pages.pin(point) orelse return -1;
    const cell = pin.rowAndCell().cell;
    if (!cell.hasText()) return 0;

    out[0] = cell.codepoint();
    var count: usize = 1;
    if (cell.hasGrapheme()) {
        if (pin.node.page().lookupGrapheme(cell)) |codepoints| {
            for (codepoints) |codepoint| {
                if (count >= buffer_size) break;
                out[count] = codepoint;
                count += 1;
            }
        }
    }
    return @intCast(count);
}

pub fn renderStateGetGrapheme(
    ptr: ?*anyopaque,
    row: c_int,
    col: c_int,
    out: [*]u32,
    buffer_size: usize,
) callconv(lib.calling_conv) c_int {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return -1));
    return getGrapheme(wrapper, .active, row, col, out, buffer_size);
}

fn getHyperlinkUri(
    wrapper: *const TerminalWrapper,
    comptime location: enum { active, history },
    y: c_int,
    x: c_int,
    out: [*]u8,
    buffer_size: usize,
) c_int {
    if (y < 0 or x < 0) return -1;
    const terminal = terminal_c.zigTerminal(wrapper.terminal) orelse return -1;
    const point = switch (location) {
        .active => @import("../point.zig").Point{ .active = .{ .x = @intCast(x), .y = @intCast(y) } },
        .history => @import("../point.zig").Point{ .history = .{ .x = @intCast(x), .y = @intCast(y) } },
    };
    const pin = terminal.screens.active.pages.pin(point) orelse return -1;
    const page = pin.node.page();
    const cell = pin.rowAndCell().cell;
    const id = page.lookupHyperlink(cell) orelse return 0;
    const entry = page.hyperlink_set.get(page.memory, id);
    const uri = entry.uri.slice(page.memory);
    const count = @min(uri.len, buffer_size);
    @memcpy(out[0..count], uri[0..count]);
    return @intCast(count);
}

pub fn renderStateGetHyperlinkUri(
    ptr: ?*anyopaque,
    row: c_int,
    col: c_int,
    out: [*]u8,
    buffer_size: usize,
) callconv(lib.calling_conv) c_int {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return -1));
    return getHyperlinkUri(wrapper, .active, row, col, out, buffer_size);
}

pub fn isAlternateScreen(ptr: ?*anyopaque) callconv(lib.calling_conv) bool {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return false));
    const terminal = terminal_c.zigTerminal(wrapper.terminal) orelse return false;
    return terminal.screens.active_key == .alternate;
}

pub fn hasMouseTracking(ptr: ?*anyopaque) callconv(lib.calling_conv) bool {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return false));
    const terminal = terminal_c.zigTerminal(wrapper.terminal) orelse return false;
    return terminal.modes.get(.mouse_event_x10) or
        terminal.modes.get(.mouse_event_normal) or
        terminal.modes.get(.mouse_event_button) or
        terminal.modes.get(.mouse_event_any);
}

pub fn getMode(ptr: ?*anyopaque, mode: c_int, is_ansi: bool) callconv(lib.calling_conv) bool {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return false));
    if (mode < 0 or mode > std.math.maxInt(u16)) return false;
    const terminal = terminal_c.zigTerminal(wrapper.terminal) orelse return false;
    const value = modespkg.modeFromInt(@intCast(mode), is_ansi) orelse return false;
    return terminal.modes.get(value);
}

pub fn getScrollbackLength(ptr: ?*anyopaque) callconv(lib.calling_conv) c_int {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return 0));
    const terminal = terminal_c.zigTerminal(wrapper.terminal) orelse return 0;
    const total = terminal.screens.active.pages.total_rows;
    return @intCast(total -| terminal.rows);
}

pub fn getScrollbackLine(
    ptr: ?*anyopaque,
    offset: c_int,
    out: [*]Cell,
    buffer_size: usize,
) callconv(lib.calling_conv) c_int {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return -1));
    if (offset < 0 or offset >= getScrollbackLength(ptr)) return -1;
    const terminal = terminal_c.zigTerminal(wrapper.terminal) orelse return -1;
    const cols: usize = wrapper.render_state.cols;
    if (buffer_size < cols) return -1;
    const pin = terminal.screens.active.pages.pin(.{ .history = .{ .y = @intCast(offset) } }) orelse return -1;
    for (0..cols) |x| out[x] = fillCell(wrapper, pin, x);
    return @intCast(cols);
}

pub fn getScrollbackGrapheme(
    ptr: ?*anyopaque,
    offset: c_int,
    col: c_int,
    out: [*]u32,
    buffer_size: usize,
) callconv(lib.calling_conv) c_int {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return -1));
    if (offset < 0 or offset >= getScrollbackLength(ptr)) return -1;
    return getGrapheme(wrapper, .history, offset, col, out, buffer_size);
}

pub fn getScrollbackHyperlinkUri(
    ptr: ?*anyopaque,
    offset: c_int,
    col: c_int,
    out: [*]u8,
    buffer_size: usize,
) callconv(lib.calling_conv) c_int {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return -1));
    if (offset < 0 or offset >= getScrollbackLength(ptr)) return -1;
    return getHyperlinkUri(wrapper, .history, offset, col, out, buffer_size);
}

pub fn isRowWrapped(ptr: ?*anyopaque, row: c_int) callconv(lib.calling_conv) bool {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return false));
    if (row < 0) return false;
    const terminal = terminal_c.zigTerminal(wrapper.terminal) orelse return false;
    const pin = terminal.screens.active.pages.pin(.{ .active = .{ .y = @intCast(row) } }) orelse return false;
    return pin.rowAndCell().row.wrap_continuation;
}

pub fn hasResponse(ptr: ?*anyopaque) callconv(lib.calling_conv) bool {
    const wrapper: *const TerminalWrapper = @ptrCast(@alignCast(ptr orelse return false));
    return wrapper.response_buffer.items.len > 0;
}

pub fn readResponse(
    ptr: ?*anyopaque,
    out: [*]u8,
    buffer_size: usize,
) callconv(lib.calling_conv) c_int {
    const wrapper: *TerminalWrapper = @ptrCast(@alignCast(ptr orelse return -1));
    const count = @min(wrapper.response_buffer.items.len, buffer_size);
    if (count == 0) return 0;
    @memcpy(out[0..count], wrapper.response_buffer.items[0..count]);
    if (count == wrapper.response_buffer.items.len) {
        wrapper.response_buffer.clearRetainingCapacity();
    } else {
        std.mem.copyForwards(u8, wrapper.response_buffer.items, wrapper.response_buffer.items[count..]);
        wrapper.response_buffer.shrinkRetainingCapacity(wrapper.response_buffer.items.len - count);
    }
    return @intCast(count);
}
