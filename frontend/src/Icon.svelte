<script lang="ts">
  import {
    Search, Plus, X, ChevronRight, ChevronDown, RefreshCw, Command, Keyboard, LogOut, FunnelX,
    ArrowUp, ArrowDown, CornerDownLeft, Tag, User, CircleDashed, Circle, CircleDot, GitPullRequest,
    OctagonAlert, CircleCheck, CircleSlash, SquareCheck, Bug, Sparkles, History, TriangleAlert, Info, Check,
    Hash, UserX, CircleDotDashed, UserPlus, ChevronLeft, Ellipsis, ListFilter, Eye, Shield, Terminal,
  } from '@lucide/svelte';

  let { name, size = 16, strokeWidth = 1.75 }: { name: string; size?: number; strokeWidth?: number } = $props();
  const icons = {
    search: Search, plus: Plus, close: X, arrow: ChevronRight, down: ChevronDown, refresh: RefreshCw,
    command: Command, keyboard: Keyboard, logout: LogOut, 'clear-filter': FunnelX, up: ArrowUp,
    'arrow-down': ArrowDown, enter: CornerDownLeft, tag: Tag, person: User, activity: History,
    warning: TriangleAlert, info: Info, check: Check, hash: Hash, unassigned: UserX, status: CircleDotDashed, 'add-person': UserPlus,
    back: ChevronLeft, more: Ellipsis, filter: ListFilter, eye: Eye, shield: Shield, terminal: Terminal,
    // Statuses
    backlog: CircleDashed, todo: Circle, in_progress: CircleDot, code_review: GitPullRequest,
    blocked: OctagonAlert, complete: CircleCheck, void: CircleSlash,
    // Item types
    task: SquareCheck, bug: Bug, feature: Sparkles,
  };
  let Glyph = $derived(icons[name as keyof typeof icons] || Circle);
</script>

<Glyph {size} {strokeWidth} aria-hidden="true" focusable="false" />
