import { useEffect, useState } from "react";
import { Button } from "../components/ui/button";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "../components/ui/table";
import {
	cleanupAttachments,
	deleteAttachment,
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
	const load = (next = cursor) =>
		listAttachments(next)
			.then((page) => { setItems(page.items); setNextCursor(page.next_cursor); setCursor(next) })
			.catch((e) => setError(e.message));
	useEffect(() => {
		void load("");
	}, []);
	const remove = async (id: string) => {
		if (window.confirm("确定删除此临时附件？正在编辑的会话可能中断。")) {
			await deleteAttachment(id);
			void load();
		}
	};
	return (
		<section className="flex h-full min-h-0 w-full flex-col gap-6">
			<header className="flex items-center justify-between">
				<div>
					<h2 className="text-lg font-semibold">临时附件</h2>
					<p className="text-sm text-muted-foreground">
						查看和管理 Gateway 临时托管的文档。
					</p>
				</div>
				<Button
					variant="outline"
					onClick={async () => {
						await cleanupAttachments();
						void load();
					}}
				>
					清理已过期附件
				</Button>
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
								<TableCell className="space-x-2 text-right">
									{!x.direct_source && (
										<Button
											asChild
											size="sm"
											variant="outline"
										>
											<a
												href={`/admin/api/attachments/${encodeURIComponent(x.document_id)}/download`}
												target="_blank"
											>
												下载
											</a>
										</Button>
									)}
									<Button
										size="sm"
										variant="outline"
										onClick={async () => {
											await extendAttachmentTTL(
												x.document_id,
												24,
											);
											void load();
										}}
									>
										延长 24h
									</Button>
									<Button
										size="sm"
										variant="destructive"
										onClick={() =>
											void remove(x.document_id)
										}
									>
										删除
									</Button>
								</TableCell>
							</TableRow>
						))}
					</TableBody>
				</Table>
			</div>
			<div className="flex justify-end gap-2"><Button variant="outline" disabled={!history.length} onClick={() => { const previous = history[history.length - 1]; setHistory(history.slice(0, -1)); void load(previous) }}>上一页</Button><Button variant="outline" disabled={!nextCursor} onClick={() => { setHistory([...history, cursor]); void load(nextCursor) }}>下一页</Button></div>
		</section>
	);
}
