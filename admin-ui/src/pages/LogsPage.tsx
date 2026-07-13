import { useEffect, useState } from "react";
import { Button } from "../components/ui/button";
import { ChevronLeft, ChevronRight, RefreshCw } from "lucide-react";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "../components/ui/table";
import {
	Tooltip,
	TooltipContent,
	TooltipTrigger,
} from "../components/ui/tooltip";
import { listAuditEvents, type AuditEvent } from "../lib/api";
export default function LogsPage() {
	const [items, setItems] = useState<AuditEvent[]>([]);
	const [cursor, setCursor] = useState("");
	const [nextCursor, setNextCursor] = useState("");
	const [history, setHistory] = useState<string[]>([]);
	const [error, setError] = useState("");
	const load = (next = cursor) =>
		listAuditEvents(next)
			.then((page) => { setItems(page.items); setNextCursor(page.next_cursor); setCursor(next) })
			.catch((e) => setError(e.message));
	useEffect(() => {
		void load("");
	}, []);
	return (
		<section className="flex h-full min-h-0 w-full flex-col gap-6">
			<header className="flex items-center justify-between">
				<div>
					<h2 className="text-lg font-semibold">运行日志</h2>
					<p className="text-sm text-muted-foreground">
						当前 Gateway 实例的结构化审计日志。
					</p>
				</div>
				<Tooltip>
					<TooltipTrigger asChild>
						<Button variant="outline" size="icon" aria-label="刷新日志" onClick={() => void load()}>
							<RefreshCw className="h-4 w-4" />
						</Button>
					</TooltipTrigger>
					<TooltipContent>刷新日志</TooltipContent>
				</Tooltip>
			</header>
			{error && <p className="text-sm text-destructive">{error}</p>}
			<div className="min-h-[200px] flex-1 overflow-auto rounded-lg border bg-card">
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead>时间</TableHead>
							<TableHead>事件</TableHead>
							<TableHead>文档</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{items.map((x, i) => (
							<TableRow key={`${x.time}-${i}`}>
								<TableCell className="text-muted-foreground">
									{new Date(x.time).toLocaleString()}
								</TableCell>
								<TableCell>{x.type}</TableCell>
								<TableCell>{x.document_id || "—"}</TableCell>
							</TableRow>
						))}
					</TableBody>
				</Table>
			</div>
			<div className="flex justify-end gap-2">
				<Tooltip>
					<TooltipTrigger asChild>
						<Button variant="outline" size="icon" aria-label="上一页" disabled={!history.length} onClick={() => { const previous = history[history.length - 1]; setHistory(history.slice(0, -1)); void load(previous) }}>
							<ChevronLeft className="h-4 w-4" />
						</Button>
					</TooltipTrigger>
					<TooltipContent>上一页</TooltipContent>
				</Tooltip>
				<Tooltip>
					<TooltipTrigger asChild>
						<Button variant="outline" size="icon" aria-label="下一页" disabled={!nextCursor} onClick={() => { setHistory([...history, cursor]); void load(nextCursor) }}>
							<ChevronRight className="h-4 w-4" />
						</Button>
					</TooltipTrigger>
					<TooltipContent>下一页</TooltipContent>
				</Tooltip>
			</div>
		</section>
	);
}
