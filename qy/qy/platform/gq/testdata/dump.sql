COPY public.pack (id, slug, title, "tIds", published) FROM stdin;
1	a	Кубок 	{8380,8382}	t
2	b	Черновик	{}	f
\.

COPY public.pack_editors ("packId", "personId", alias) FROM stdin;
1	8	\N
2	7	\N
\.

COPY public.person (id, slug, name, "playerId") FROM stdin;
7	p	Пётр Петров	707
8	q	Иван Иванов	\N
\.

COPY public.question (id, number, text, "razdatkaText", "razdatkaPic", answer, zachet, nezachet, comment, source, "takenDown", "packId", "tourId") FROM stdin;
10	1	Вопрос\\про\nИКС.	Текст	/pics/1/10.png	икс	\N				f	1	100
11	2	Снят			а					t	1	100
12	1	Черновой			б					f	2	\N
13	13	Второй тур			в					f	1	101
\.

COPY public.question_authors ("questionId", "personId", alias) FROM stdin;
10	7	\N
10	8	Ваня
\.

COPY public.tour_editors ("tourId", "personId", alias) FROM stdin;
100	7	\N
\.
