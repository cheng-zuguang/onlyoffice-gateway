import { useEffect, useState } from "react";
import { Button } from "../components/ui/button";
import { ConfirmDialog } from "../components/ui/confirm-dialog";
import { toast } from "sonner";
import { ChevronLeft, ChevronRight, Download, RotateCcw, Trash2, TimerReset } from "lucide-react";
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
import {
	cleanupAttachments,
	deleteAttachment,
	downloadAttachment,
	extendAttachmentTTL,
	listAttachments,
	type Attachment,
} from "../lib/api";

export default function AttachmentsPage() {
	const [items, setItems] = useState<Attachment[]>([]);
	const [cursor, setCursor] = useState("");
	const [nextCursor, setNextCursor] = useState("");
	const [history, setHistory] = useState<string[]>([]);
	const [error, setError] = useState("");
	const [deleteTarget, setDeleteTarget] = useState<Attachment | null>(null);
	const load = (next = cursor) =>
		listAttachments(next)
			.then((page) => { setItems(page.items); setNextCursor(page.next_cursor); setCursor(next) })
			.catch((e) => setError(e.message));
	useEffect(() => {
		void load("");
	}, []);
	const remove = async () => { if (!deleteTarget) return; try { await deleteAttachment(deleteTarget.document_id); toast.success("临时附件已删除"); await load() } catch (e) { toast.error(e instanceof Error ? e.message : "删除临时附件失败") } finally { setDeleteTarget(null) } };
	const download = async (attachment: Attachment) => { try { const { blob, fileName } = await downloadAttachment(attachment.document_id); const url = URL.createObjectURL(blob); const link = document.createElement("a"); link.href = url; link.download = fileName || attachment.file_name; link.click(); URL.revokeObjectURL(url); toast.success("开始下载附件") } catch (e) { toast.error(e instanceof Error ? e.message : "下载临时附件失败") } };
	const extendTTL = async (attachment: Attachment) => {
		try { await extendAttachmentTTL(attachment.document_id, 24); toast.success("已延长 24 小时"); await load() } catch (e) { toast.error(e instanceof Error ? e.message : "延长有效期失败") }
	};
	return (
		<section className="flex h-full min-h-0 w-full flex-col gap-6">
			<ConfirmDialog open={deleteTarget !== null} title="删除临时附件" message={`确定删除“${deleteTarget?.file_name || deleteTarget?.document_id || ""}”吗？正在编辑的会话可能中断。`} confirmLabel="删除" variant="destructive" onConfirm={() => void remove()} onCancel={() => setDeleteTarget(null)} />
			<header className="flex items-center justify-between">
				<div>
					<h2 className="text-lg font-semibold">临时附件</h2>
					<p className="text-sm text-muted-foreground">
						查看和管理 Gateway 临时托管的文档。
					</p>
				</div>
				<Tooltip>
					<TooltipTrigger asChild>
						<Button
							variant="outline"
							size="icon"
							aria-label="清理已过期附件"
							onClick={async () => {
								try { const cleaned = await cleanupAttachments(); toast.success(`已清理 ${cleaned} 个过期附件`); await load() } catch (e) { toast.error(e instanceof Error ? e.message : "清理过期附件失败") }
							}}
						>
							<RotateCcw className="h-4 w-4" />
						</Button>
					</TooltipTrigger>
					<TooltipContent>清理已过期附件</TooltipContent>
				</Tooltip>
			</header>
			{error && <p className="text-sm text-destructive">{error}</p>}
			<div className="min-h-[200px] flex-1 overflow-auto rounded-lg border bg-card">
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead>文件</TableHead>
							<TableHead>服务</TableHead>
							<TableHead>到期时间</TableHead>
							<TableHead className="text-right">操作</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{items.map((x) => (
							<TableRow key={x.document_id}>
								<TableCell>
									<div>{x.file_name || x.document_id}</div>
									<div className="text-xs text-muted-foreground">
										{x.direct_source
											? `直连：${x.source_host || "外部存储"}`
											: x.is_edited
												? "已编辑"
												: "原件"}
									</div>
								</TableCell>
								<TableCell>{x.service_id}</TableCell>
								<TableCell>
									{new Date(x.expires_at).toLocaleString()}
								</TableCell>
								<TableCell className="text-right">
									<div className="flex items-center justify-end gap-1">
										{!x.direct_source && (
											<Tooltip>
												<TooltipTrigger asChild>
													<Button size="icon" variant="ghost" aria-label="下载附件" onClick={() => void download(x)}>
														<Download className="h-4 w-4" />
													</Button>
												</TooltipTrigger>
												<TooltipContent>下载附件</TooltipContent>
											</Tooltip>
										)}
										<Tooltip>
											<TooltipTrigger asChild>
												<Button
													size="icon"
													variant="ghost"
													aria-label="延长 24 小时"
													onClick={() => void extendTTL(x)}
												>
													<TimerReset className="h-4 w-4" />
												</Button>
											</TooltipTrigger>
											<TooltipContent>延长 24 小时</TooltipContent>
										</Tooltip>
										<Tooltip>
											<TooltipTrigger asChild>
												<Button
													size="icon"
													variant="ghost"
													aria-label="删除附件"
													onClick={() => setDeleteTarget(x)}
												>
													<Trash2 className="h-4 w-4 text-destructive" />
												</Button>
											</TooltipTrigger>
											<TooltipContent>删除附件</TooltipContent>
										</Tooltip>
									</div>
								</TableCell>
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
