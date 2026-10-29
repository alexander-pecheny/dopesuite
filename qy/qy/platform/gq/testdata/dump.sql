COPY public.pack (id, slug, title, "tIds", published) FROM stdin;
1	a	Кубок 	{8380,8382}	t
2	b	Черновик	{}	f
\.

COPY public.person (id, slug, name) FROM stdin;
7	p	Пётр Петров
8	q	Иван Иванов
\.

COPY public.question (id, number, text, "razdatkaText", "razdatkaPic", answer, zachet, nezachet, comment, source, "takenDown", "packId") FROM stdin;
10	1	Вопрос\\про\nИКС.	Текст	/pics/1/10.png	икс	\N				f	1
11	2	Снят			а					t	1
12	1	Черновой			б					f	2
\.

COPY public.question_authors ("questionId", "personId", alias) FROM stdin;
10	7	\N
10	8	Ваня
\.
